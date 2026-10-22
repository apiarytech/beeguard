/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/evaluator"
)

// TestSnapshotRestore: alarm states survive a restart of the engine: an
// acknowledged alarm stays acknowledged, a shelve stays, a latch stays, and
// an alarm still active is not activated again.
func TestSnapshotRestore(t *testing.T) {
	e, c, _ := newEngine(t)
	e.Process("Guard1.Lid", sample(1, c.Now()))
	if err := e.Ack("Guard1.LidOpen", "olga"); err != nil {
		t.Fatal(err)
	}
	if err := e.Shelve("Guard1.TempHigh", "olga", 30*time.Minute, false); err != nil {
		t.Fatal(err)
	}
	e.Process("Guard1.Temp", sample(41, c.Now()))
	c.Advance(time.Second)
	e.Process("Guard1.Temp", sample(30, c.Now()))

	// The states go through JSON, as a file would hold them.
	raw, err := json.Marshal(e.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]alarm.Snapshot
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}

	again, c2, r2 := newEngine(t)
	c2.now = c.Now()
	if err := again.Restore(saved); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]alarm.State{
		"Guard1.LidOpen":      alarm.ActiveAcked,
		"Guard1.TempHigh":     alarm.Shelved,
		"Guard1.TempHighHigh": alarm.LatchedUnacked,
	} {
		if s, _ := again.Status(id); s.State != want {
			t.Errorf("%s restored as %s, want %s", id, s.State, want)
		}
	}
	// The lid is still open: no new activation. It closes: RTN.
	again.Process("Guard1.Lid", sample(1, c2.Now()))
	if len(r2.kinds()) != 0 {
		t.Fatalf("an alarm still active was activated again: %v", r2.kinds())
	}
	again.Process("Guard1.Lid", sample(0, c2.Now()))
	if k := r2.kinds(); len(k) != 1 || k[0] != alarm.ReturnedToNormal {
		t.Errorf("events after the lid closed: %v", k)
	}

	// A state that does not fit the definition is refused; unknown IDs are ignored.
	bad := map[string]alarm.Snapshot{
		"Guard1.LidOpen": {Base: alarm.LatchedUnacked},
		"Gone.Alarm":     {Base: alarm.ActiveUnacked},
	}
	fresh, _, _ := newEngine(t)
	if err := fresh.Restore(bad); err == nil {
		t.Error("restored a latched state into an alarm that is not latched")
	}
}

// TestRestoreIssue5 covers issue #5: a restored active alarm returns to
// normal on the first normal sample, a shelve the definition doesn't allow
// is refused and an allowed one is capped, and stale chattering is dropped.
func TestRestoreIssue5(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)}
	def := func(maxShelve time.Duration, chatter int) []Definition {
		return []Definition{{
			Alarm:     alarm.Config{ID: "A", Severity: 500, MaxShelve: maxShelve, ChatterCount: chatter, ChatterWindow: time.Minute},
			Source:    "S",
			Condition: evaluator.Config{Kind: evaluator.Digital},
		}}
	}
	newE := func(defs []Definition) *Engine {
		e, err := New(defs, WithClock(c.Now))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}

	// 1. Active when saved, normal at the first sample after the restart.
	e := newE(def(time.Hour, 0))
	e.Process("S", sample(1, c.Now()))
	saved := e.Snapshot()
	again := newE(def(time.Hour, 0))
	if err := again.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if s, _ := again.Status("A"); s.State != alarm.ActiveAcked {
		t.Fatalf("restored state = %v, want ACTIVE_ACK", s.State)
	}
	c.Advance(time.Second)
	again.Process("S", sample(0, c.Now()))
	if s, _ := again.Status("A"); s.State != alarm.Normal {
		t.Errorf("after a normal sample: %v, want NORMAL (the restored alarm stuck)", s.State)
	}

	// 2. A shelve: refused when the definition no longer allows shelving,
	// capped at the shelve's start plus the current MaxShelve otherwise.
	e = newE(def(24*time.Hour, 0))
	if err := e.Shelve("A", "op", 0, false); err != nil {
		t.Fatal(err)
	}
	saved = e.Snapshot()
	if err := newE(def(0, 0)).Restore(saved); err == nil {
		t.Error("a shelve was restored into an alarm that may not be shelved")
	}
	capped := newE(def(time.Hour, 0))
	if err := capped.Restore(saved); err != nil {
		t.Fatal(err)
	}
	if got := capped.Snapshot()["A"].ShelveExpiry; !got.Equal(saved["A"].ShelveTime.Add(time.Hour)) {
		t.Errorf("shelve expiry = %v, want start + 1h", got)
	}

	// 3. Chattering restored into an alarm without chatter detection is dropped.
	snap := saved["A"]
	snap.Shelved, snap.Chattering, snap.Rises = false, true, []time.Time{c.Now(), c.Now(), c.Now()}
	plain := newE(def(time.Hour, 0))
	if err := plain.Restore(map[string]alarm.Snapshot{"A": snap}); err != nil {
		t.Fatal(err)
	}
	if s, _ := plain.Status("A"); s.Chattering {
		t.Error("chattering restored into an alarm without chatter detection")
	}
}
