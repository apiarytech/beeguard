/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package journal

import (
	"context"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func events() []alarm.Event {
	return []alarm.Event{
		{Time: t0, Alarm: "A", Kind: alarm.Activated},
		{Time: t0.Add(time.Second), Alarm: "B", Kind: alarm.Activated},
		{Time: t0.Add(2 * time.Second), Alarm: "A", Kind: alarm.Acknowledged, User: "op"},
		{Time: t0.Add(3 * time.Second), Alarm: "A", Kind: alarm.ReturnedToNormal},
	}
}

func TestMemoryQuery(t *testing.T) {
	ctx := context.Background()
	m := NewMemory(0)
	if err := m.Publish(ctx, events()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"all", Filter{}, 4},
		{"alarm", Filter{Alarm: "A"}, 3},
		{"kinds", Filter{Kinds: []alarm.EventKind{alarm.Activated}}, 2},
		{"since", Filter{Since: t0.Add(time.Second)}, 3},
		{"until", Filter{Until: t0.Add(time.Second)}, 1},
		{"limit", Filter{Alarm: "A", Limit: 2}, 2},
	}
	for _, c := range cases {
		got, err := m.Query(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: got %d events (%v), want %d", c.name, len(got), err, c.want)
		}
	}
	got, _ := m.Query(ctx, Filter{Alarm: "A", Limit: 2})
	if got[0].Kind != alarm.Acknowledged || got[1].Kind != alarm.ReturnedToNormal {
		t.Fatalf("Limit must keep the most recent events, oldest first: %v", got)
	}
}

func TestMemoryMax(t *testing.T) {
	m := NewMemory(3)
	_ = m.Publish(context.Background(), events())
	got, _ := m.Query(context.Background(), Filter{})
	if len(got) != 3 || got[0].Alarm != "B" {
		t.Fatalf("got %v", got)
	}
}
