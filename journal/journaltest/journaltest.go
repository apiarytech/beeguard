/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package journaltest checks a journal.Journal: paging and retention, the
// same for every implementation.
package journaltest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/journal"
)

// Run publishes ten events a minute apart into an empty j and checks
// Offset, Limit and Oldest, then Purge if j is a journal.Purger.
func Run(t *testing.T, j journal.Journal) {
	t.Helper()
	ctx := context.Background()
	t0 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	var evs []alarm.Event
	for i := range 10 {
		evs = append(evs, alarm.Event{Time: t0.Add(time.Duration(i) * time.Minute), Alarm: fmt.Sprintf("A%d", i),
			Kind: alarm.Activated, State: alarm.ActiveUnacked, Previous: alarm.Normal, Priority: alarm.High})
	}
	if err := j.Publish(ctx, evs[:6]); err != nil {
		t.Fatal(err)
	}
	if err := j.Publish(ctx, evs[6:]); err != nil {
		t.Fatal(err)
	}
	check := func(f journal.Filter, want ...int) {
		t.Helper()
		got, err := j.Query(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var names, wantNames []string
		for _, e := range got {
			names = append(names, e.Alarm)
		}
		for _, i := range want {
			wantNames = append(wantNames, fmt.Sprintf("A%d", i))
		}
		if fmt.Sprint(names) != fmt.Sprint(wantNames) {
			t.Errorf("%+v: %v, want %v", f, names, wantNames)
		}
	}
	// Pages from the oldest.
	check(journal.Filter{Oldest: true, Limit: 4}, 0, 1, 2, 3)
	check(journal.Filter{Oldest: true, Limit: 4, Offset: 4}, 4, 5, 6, 7)
	check(journal.Filter{Oldest: true, Limit: 4, Offset: 8}, 8, 9)
	check(journal.Filter{Oldest: true, Limit: 4, Offset: 10})
	check(journal.Filter{Oldest: true, Offset: 7}, 7, 8, 9)
	// Pages from the most recent, oldest first.
	check(journal.Filter{Limit: 3}, 7, 8, 9)
	check(journal.Filter{Limit: 3, Offset: 3}, 4, 5, 6)
	check(journal.Filter{Offset: 8}, 0, 1)
	// A day's range, in pages.
	day := journal.Filter{Since: t0.Add(2 * time.Minute), Until: t0.Add(8 * time.Minute), Oldest: true, Limit: 4}
	check(day, 2, 3, 4, 5)
	day.Offset = 4
	check(day, 6, 7)

	p, ok := j.(journal.Purger)
	if !ok {
		return
	}
	n, err := p.Purge(ctx, t0.Add(5*time.Minute))
	if err != nil || n != 5 {
		t.Fatalf("purge: %d, %v; want 5", n, err)
	}
	check(journal.Filter{}, 5, 6, 7, 8, 9)
	if n, err := p.Purge(ctx, t0.Add(5*time.Minute)); err != nil || n != 0 {
		t.Errorf("purge again: %d, %v; want 0", n, err)
	}
	// Still recording after a purge.
	late := evs[9]
	late.Alarm, late.Time = "A10", t0.Add(10*time.Minute)
	if err := j.Publish(ctx, []alarm.Event{late}); err != nil {
		t.Fatal(err)
	}
	check(journal.Filter{Oldest: true, Offset: 4}, 9, 10)
}
