/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package sqljournal stores the alarm journal in a SQL database through
// database/sql. It supports the databases honeycomb's sqlstore does: sqlite,
// postgres, cockroachdb, mysql and sqlserver. Bring the driver, e.g.
//
//	import _ "modernc.org/sqlite"
//	db, _ := sql.Open("sqlite", "beeguard.db")
//	j, _ := sqljournal.New(ctx, db, "sqlite")
//
// Times are stored as Unix nanoseconds in UTC, so every database sorts and
// compares them the same way.
package sqljournal

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/journal"
)

// Table is the name of the journal table.
const Table = "beeguard_events"

const columns = "event_time, source_time, alarm, description, kind, state, previous, priority, user_name, detail"

var schemas = map[string][]string{
	"sqlite": {
		`CREATE TABLE IF NOT EXISTS beeguard_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT, event_time INTEGER NOT NULL, source_time INTEGER NOT NULL,
			alarm TEXT NOT NULL, description TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL,
			previous TEXT NOT NULL, priority INTEGER NOT NULL, user_name TEXT NOT NULL, detail TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_beeguard_events_alarm ON beeguard_events (alarm, event_time)`,
		`CREATE INDEX IF NOT EXISTS ix_beeguard_events_time ON beeguard_events (event_time)`,
	},
	"postgres": {
		`CREATE TABLE IF NOT EXISTS beeguard_events (
			id BIGSERIAL PRIMARY KEY, event_time BIGINT NOT NULL, source_time BIGINT NOT NULL,
			alarm VARCHAR(255) NOT NULL, description VARCHAR(1024) NOT NULL, kind VARCHAR(32) NOT NULL,
			state VARCHAR(32) NOT NULL, previous VARCHAR(32) NOT NULL, priority SMALLINT NOT NULL,
			user_name VARCHAR(255) NOT NULL, detail VARCHAR(1024) NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_beeguard_events_alarm ON beeguard_events (alarm, event_time)`,
		`CREATE INDEX IF NOT EXISTS ix_beeguard_events_time ON beeguard_events (event_time)`,
	},
	"mysql": {
		`CREATE TABLE IF NOT EXISTS beeguard_events (
			id BIGINT AUTO_INCREMENT PRIMARY KEY, event_time BIGINT NOT NULL, source_time BIGINT NOT NULL,
			alarm VARCHAR(255) NOT NULL, description VARCHAR(1024) NOT NULL, kind VARCHAR(32) NOT NULL,
			state VARCHAR(32) NOT NULL, previous VARCHAR(32) NOT NULL, priority SMALLINT NOT NULL,
			user_name VARCHAR(255) NOT NULL, detail VARCHAR(1024) NOT NULL,
			INDEX ix_beeguard_events_alarm (alarm, event_time), INDEX ix_beeguard_events_time (event_time))`,
	},
	"sqlserver": {
		`IF OBJECT_ID(N'beeguard_events', N'U') IS NULL CREATE TABLE beeguard_events (
			id BIGINT IDENTITY(1,1) PRIMARY KEY, event_time BIGINT NOT NULL, source_time BIGINT NOT NULL,
			alarm NVARCHAR(255) NOT NULL, description NVARCHAR(1024) NOT NULL, kind NVARCHAR(32) NOT NULL,
			state NVARCHAR(32) NOT NULL, previous NVARCHAR(32) NOT NULL, priority SMALLINT NOT NULL,
			user_name NVARCHAR(255) NOT NULL, detail NVARCHAR(1024) NOT NULL,
			INDEX ix_beeguard_events_alarm (alarm, event_time), INDEX ix_beeguard_events_time (event_time))`,
	},
}

var aliases = map[string]string{"sqlite3": "sqlite", "pgx": "postgres", "cockroachdb": "postgres", "mssql": "sqlserver"}

// Journal is a journal.Journal backed by a SQL database.
type Journal struct {
	db      *sql.DB
	dialect string
	insert  string
}

var _ journal.Journal = (*Journal)(nil)

// New creates the journal table if it does not exist. dialect is one of
// sqlite, postgres, cockroachdb, mysql or sqlserver.
func New(ctx context.Context, db *sql.DB, dialect string) (*Journal, error) {
	if a, ok := aliases[dialect]; ok {
		dialect = a
	}
	schema, ok := schemas[dialect]
	if !ok {
		return nil, fmt.Errorf("sqljournal: unsupported dialect %q", dialect)
	}
	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("sqljournal: create %s: %w", Table, err)
		}
	}
	j := &Journal{db: db, dialect: dialect}
	params := make([]string, 10)
	for i := range params {
		params[i] = j.placeholder(i + 1)
	}
	j.insert = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", Table, columns, strings.Join(params, ", "))
	return j, nil
}

func (j *Journal) placeholder(n int) string {
	switch j.dialect {
	case "postgres":
		return fmt.Sprintf("$%d", n)
	case "sqlserver":
		return fmt.Sprintf("@p%d", n)
	}
	return "?"
}

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// Publish implements engine.Sink. The events are written in one transaction.
func (j *Journal) Publish(ctx context.Context, events []alarm.Event) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqljournal: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	stmt, err := tx.PrepareContext(ctx, j.insert)
	if err != nil {
		return fmt.Errorf("sqljournal: %w", err)
	}
	defer stmt.Close()
	for _, e := range events {
		if _, err := stmt.ExecContext(ctx, nanos(e.Time), nanos(e.SourceTime), e.Alarm, e.Description,
			e.Kind.String(), e.State.String(), e.Previous.String(), int(e.Priority), e.User, e.Detail); err != nil {
			return fmt.Errorf("sqljournal: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqljournal: commit: %w", err)
	}
	return nil
}

// Query returns the matching events, oldest first.
func (j *Journal) Query(ctx context.Context, f journal.Filter) ([]alarm.Event, error) {
	var where []string
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(cond, j.placeholder(len(args))))
	}
	if f.Alarm != "" {
		add("alarm = %s", f.Alarm)
	}
	if !f.Since.IsZero() {
		add("event_time >= %s", f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		add("event_time < %s", f.Until.UnixNano())
	}
	if len(f.Kinds) > 0 {
		in := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			args = append(args, k.String())
			in[i] = j.placeholder(len(args))
		}
		where = append(where, "kind IN ("+strings.Join(in, ", ")+")")
	}

	query := "SELECT " + columns + " FROM " + Table
	if f.Limit > 0 && j.dialect == "sqlserver" {
		query = fmt.Sprintf("SELECT TOP %d %s FROM %s", f.Limit, columns, Table)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC" // newest first, so Limit keeps the most recent
	if f.Limit > 0 && j.dialect != "sqlserver" {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}

	rows, err := j.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqljournal: query: %w", err)
	}
	defer rows.Close()
	var out []alarm.Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqljournal: query: %w", err)
	}
	slices.Reverse(out)
	return out, nil
}

func scan(rows *sql.Rows) (alarm.Event, error) {
	var (
		e                     alarm.Event
		eventTime, sourceTime int64
		kind, state, previous string
		priority              int
	)
	if err := rows.Scan(&eventTime, &sourceTime, &e.Alarm, &e.Description, &kind, &state, &previous,
		&priority, &e.User, &e.Detail); err != nil {
		return e, fmt.Errorf("sqljournal: scan: %w", err)
	}
	var err error
	if e.Kind, err = alarm.ParseEventKind(kind); err != nil {
		return e, err
	}
	if e.State, err = alarm.ParseState(state); err != nil {
		return e, err
	}
	if e.Previous, err = alarm.ParseState(previous); err != nil {
		return e, err
	}
	e.Time, e.SourceTime, e.Priority = fromNanos(eventTime), fromNanos(sourceTime), alarm.Priority(priority)
	return e, nil
}
