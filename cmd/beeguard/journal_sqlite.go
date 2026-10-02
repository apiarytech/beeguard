//go:build sqlite

/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// The SQLite journal adds nine third-party modules (modernc.org/sqlite and
// its dependencies), so it is only built with:
//
//	go build -tags sqlite ./cmd/beeguard

package main

import (
	"context"
	"database/sql"
	"io"

	_ "modernc.org/sqlite"

	"github.com/apiarytech/beeguard/journal"
	"github.com/apiarytech/beeguard/journal/sqljournal"
)

func init() {
	openSQLite := func(ctx context.Context, path string) (journal.Journal, io.Closer, error) {
		// Wait for locks instead of failing, so other tools can read the journal while beeguard writes it.
		db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return nil, nil, err
		}
		j, err := sqljournal.New(ctx, db, "sqlite")
		if err != nil {
			db.Close()
			return nil, nil, err
		}
		return j, db, nil
	}
	openers[".db"] = openSQLite
	openers[".sqlite"] = openSQLite
}
