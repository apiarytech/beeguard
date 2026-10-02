/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package sqljournal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/journal"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func open(t *testing.T) (*Journal, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	j, err := New(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return j, db
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	j, _ := open(t)
	in := []alarm.Event{
		{Time: t0, SourceTime: t0.Add(-time.Millisecond), Alarm: "Guard1.TempHigh", Description: "Temp high",
			Kind: alarm.Activated, State: alarm.ActiveUnacked, Previous: alarm.Normal, Priority: alarm.High},
		{Time: t0.Add(time.Second), Alarm: "Guard1.TempHigh", Kind: alarm.ShelvedEvent, State: alarm.Shelved,
			Previous: alarm.ActiveUnacked, Priority: alarm.High, User: "franklin", Detail: "for 1h0m0s"},
		{Time: t0.Add(2 * time.Second), Alarm: "Guard2.LidOpen", Kind: alarm.Activated, State: alarm.ActiveUnacked, Priority: alarm.Low},
	}
	if err := j.Publish(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := j.Query(ctx, journal.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d events", len(got))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("event %d:\n got  %+v\n want %+v", i, got[i], in[i])
		}
	}
}

func TestQueryFilters(t *testing.T) {
	ctx := context.Background()
	j, _ := open(t)
	var in []alarm.Event
	for i := range 6 {
		kind := alarm.Activated
		if i%2 == 1 {
			kind = alarm.ReturnedToNormal
		}
		name := "A"
		if i >= 4 {
			name = "B"
		}
		in = append(in, alarm.Event{Time: t0.Add(time.Duration(i) * time.Second), Alarm: name, Kind: kind})
	}
	if err := j.Publish(ctx, in); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		f    journal.Filter
		want int
	}{
		{"alarm", journal.Filter{Alarm: "A"}, 4},
		{"kinds", journal.Filter{Kinds: []alarm.EventKind{alarm.ReturnedToNormal}}, 3},
		{"since/until", journal.Filter{Since: t0.Add(time.Second), Until: t0.Add(4 * time.Second)}, 3},
		{"limit", journal.Filter{Limit: 2}, 2},
		{"combined", journal.Filter{Alarm: "A", Kinds: []alarm.EventKind{alarm.Activated}, Limit: 1}, 1},
	}
	for _, c := range cases {
		got, err := j.Query(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: got %d (%v), want %d", c.name, len(got), err, c.want)
		}
	}
	got, _ := j.Query(ctx, journal.Filter{Limit: 2})
	if !got[0].Time.Equal(t0.Add(4*time.Second)) || !got[1].Time.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("Limit must keep the most recent, oldest first: %v", got)
	}
}

func TestNewIsIdempotent(t *testing.T) {
	_, db := open(t)
	if _, err := New(context.Background(), db, "sqlite3"); err != nil {
		t.Fatalf("second New: %v", err)
	}
	if _, err := New(context.Background(), db, "oracle"); err == nil {
		t.Fatal("accepted an unsupported dialect")
	}
}

func TestPlaceholders(t *testing.T) {
	for dialect, want := range map[string]string{"sqlite": "?", "mysql": "?", "postgres": "$3", "sqlserver": "@p3"} {
		j := &Journal{dialect: dialect}
		if got := j.placeholder(3); got != want {
			t.Errorf("%s placeholder = %q, want %q", dialect, got, want)
		}
	}
}
