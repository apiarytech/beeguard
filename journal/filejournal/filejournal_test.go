/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package filejournal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/journal"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func open(t *testing.T, opts ...Option) (*Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := Open(path, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j, path
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	j, _ := open(t)
	in := []alarm.Event{
		{Time: t0, SourceTime: t0.Add(-time.Millisecond), Alarm: "Guard1.TempHigh", Description: "Temp high",
			Kind: alarm.Activated, State: alarm.ActiveUnacked, Previous: alarm.Normal, Priority: alarm.High},
		{Time: t0.Add(time.Second), Alarm: "Guard1.TempHigh", Kind: alarm.ShelvedEvent, State: alarm.Shelved,
			Previous: alarm.ActiveUnacked, Priority: alarm.High, User: "franklin", Detail: "for 1h0m0s"},
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
	j, _ := open(t, WithoutSync())
	for i := range 6 {
		kind := alarm.Activated
		if i%2 == 1 {
			kind = alarm.ReturnedToNormal
		}
		_ = j.Publish(ctx, []alarm.Event{{Time: t0.Add(time.Duration(i) * time.Second), Alarm: "A", Kind: kind}})
	}
	cases := map[string]struct {
		f    journal.Filter
		want int
	}{
		"kinds": {journal.Filter{Kinds: []alarm.EventKind{alarm.ReturnedToNormal}}, 3},
		"since": {journal.Filter{Since: t0.Add(4 * time.Second)}, 2},
		"limit": {journal.Filter{Limit: 2}, 2},
		"other": {journal.Filter{Alarm: "B"}, 0},
	}
	for name, c := range cases {
		got, err := j.Query(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: got %d (%v), want %d", name, len(got), err, c.want)
		}
	}
}

// A crash can leave the last line half-written; it is skipped. A damaged line
// in the middle is an error.
func TestDamagedLines(t *testing.T) {
	ctx := context.Background()
	j, path := open(t)
	_ = j.Publish(ctx, []alarm.Event{{Time: t0, Alarm: "A", Kind: alarm.Activated}})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"time":"2026-10-01T08:00:01Z","alarm":"A","ki`)
	f.Close()
	got, err := j.Query(ctx, journal.Filter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("partial last line: %v, %v", got, err)
	}

	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("\n")
	f.Close()
	_ = j.Publish(ctx, []alarm.Event{{Time: t0, Alarm: "A", Kind: alarm.Activated}})
	if _, err := j.Query(ctx, journal.Filter{}); err == nil {
		t.Fatal("a damaged line in the middle was accepted")
	}
}

func TestReopenAppends(t *testing.T) {
	ctx := context.Background()
	j, path := open(t)
	_ = j.Publish(ctx, []alarm.Event{{Time: t0, Alarm: "A", Kind: alarm.Activated}})
	j.Close()
	j2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	_ = j2.Publish(ctx, []alarm.Event{{Time: t0, Alarm: "A", Kind: alarm.ReturnedToNormal}})
	got, _ := j2.Query(ctx, journal.Filter{})
	if len(got) != 2 {
		t.Fatalf("got %d events after reopening", len(got))
	}
}
