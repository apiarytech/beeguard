/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package api

import (
	"context"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
	"github.com/apiarytech/beeguard/journal"
)

func newService(t *testing.T) (Service, *engine.Engine) {
	t.Helper()
	j := journal.NewMemory(0)
	eng, err := engine.New([]engine.Definition{
		{
			Alarm:     alarm.Config{ID: "Guard1.TempHigh", Severity: 700, AckRequired: true, MaxShelve: time.Hour},
			Source:    "Guard1.Temp",
			Condition: evaluator.Config{Kind: evaluator.High, Limit: 38},
		},
		{
			Alarm:     alarm.Config{ID: "Guard1.LidOpen", Severity: 300, AckRequired: true},
			Source:    "Guard1.Lid",
			Condition: evaluator.Config{Kind: evaluator.Digital},
		},
	}, engine.WithSink(j))
	if err != nil {
		t.Fatal(err)
	}
	return New(eng, j), eng
}

func TestListAndGet(t *testing.T) {
	svc, eng := newService(t)
	ctx := context.Background()
	eng.Process("Guard1.Temp", evaluator.Sample{Value: 39, Good: true})
	all, _ := svc.ListAlarms(ctx)
	if len(all) != 2 {
		t.Fatalf("alarms = %d", len(all))
	}
	active, _ := svc.ActiveAlarms(ctx)
	if len(active) != 1 || active[0].ID != "Guard1.TempHigh" || active[0].State != "ACTIVE_UNACK" ||
		active[0].ISA != "B" || active[0].InAlarmTime == nil || active[0].Priority != 2 {
		t.Fatalf("active = %+v", active)
	}
	if _, err := svc.GetAlarm(ctx, "Nope"); CodeOf(err) != CodeNotFound {
		t.Fatalf("GetAlarm unknown: %v", err)
	}
}

func TestCommands(t *testing.T) {
	svc, eng := newService(t)
	ctx := context.Background()
	eng.Process("Guard1.Temp", evaluator.Sample{Value: 39, Good: true})

	a, err := svc.Command(ctx, CommandRequest{Alarm: "Guard1.TempHigh", Command: CmdAck, User: "franklin"})
	if err != nil || !a.Acked {
		t.Fatalf("ack: %+v, %v", a, err)
	}
	a, err = svc.Command(ctx, CommandRequest{Alarm: "Guard1.TempHigh", Command: CmdShelve, User: "franklin", ShelveMinutes: 30})
	if err != nil || !a.Shelved || a.ShelveUntil == nil {
		t.Fatalf("shelve: %+v, %v", a, err)
	}
	for _, cmd := range []Command{CmdUnshelve, CmdSuppress, CmdUnsuppress, CmdDisable, CmdEnable, CmdResetCount} {
		if _, err := svc.Command(ctx, CommandRequest{Alarm: "Guard1.TempHigh", Command: cmd, User: "franklin"}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}

	cases := []struct {
		req  CommandRequest
		want Code
	}{
		{CommandRequest{Alarm: "Guard1.TempHigh", Command: CmdAck}, CodeInvalid},               // no user
		{CommandRequest{Alarm: "Guard1.TempHigh", Command: "dance", User: "u"}, CodeInvalid},   // unknown command
		{CommandRequest{Alarm: "Nope", Command: CmdAck, User: "u"}, CodeNotFound},              // unknown alarm
		{CommandRequest{Alarm: "Guard1.LidOpen", Command: CmdShelve, User: "u"}, CodeRejected}, // shelving not permitted
		{CommandRequest{Alarm: "Guard1.LidOpen", Command: CmdReset, User: "u"}, CodeRejected},  // not latched
		{CommandRequest{Alarm: "Guard1.TempHigh", Command: CmdShelve, User: "u", ShelveMinutes: -1}, CodeInvalid},
	}
	for _, c := range cases {
		if _, err := svc.Command(ctx, c.req); CodeOf(err) != c.want {
			t.Errorf("%+v: code %v (%v), want %v", c.req, CodeOf(err), err, c.want)
		}
	}
}

func TestQueryEvents(t *testing.T) {
	svc, eng := newService(t)
	ctx := context.Background()
	eng.Process("Guard1.Temp", evaluator.Sample{Value: 39, Good: true, Time: time.Now()})
	_, _ = svc.Command(ctx, CommandRequest{Alarm: "Guard1.TempHigh", Command: CmdAck, User: "franklin"})

	events, err := svc.QueryEvents(ctx, EventQuery{Kinds: []string{"ACKNOWLEDGED"}})
	if err != nil || len(events) != 1 || events[0].User != "franklin" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	all, _ := svc.QueryEvents(ctx, EventQuery{})
	if len(all) != 2 || all[0].SourceTime == nil {
		t.Fatalf("all = %+v", all)
	}
	if _, err := svc.QueryEvents(ctx, EventQuery{Kinds: []string{"EXPLODED"}}); CodeOf(err) != CodeInvalid {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := svc.QueryEvents(ctx, EventQuery{Limit: -1}); CodeOf(err) != CodeInvalid {
		t.Fatalf("bad limit: %v", err)
	}
}
