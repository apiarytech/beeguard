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
