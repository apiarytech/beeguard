/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package alarm

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

func newAlarm(t *testing.T, cfg Config) *Alarm {
	t.Helper()
	if cfg.ID == "" {
		cfg.ID = "TT101.HI"
	}
	if cfg.Severity == 0 {
		cfg.Severity = 500
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func wantState(t *testing.T, a *Alarm, want State) {
	t.Helper()
	if got := a.State(); got != want {
		t.Fatalf("state = %v, want %v", got, want)
	}
}

func wantKinds(t *testing.T, events []Event, want ...EventKind) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events = %v, want kinds %v", events, want)
	}
	for i, e := range events {
		if e.Kind != want[i] {
			t.Fatalf("event %d = %v, want %v (all: %v)", i, e.Kind, want[i], events)
		}
	}
}

func mustAck(t *testing.T, a *Alarm, when time.Time) {
	t.Helper()
	if _, err := a.Ack("op", when); err != nil {
		t.Fatal(err)
	}
}

// The four combinations of acknowledge-required and latched.
func TestAckRequiredNotLatched(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true})
	wantKinds(t, a.SetCondition(true, at(1)), Activated)
	wantState(t, a, ActiveUnacked)
	wantKinds(t, a.SetCondition(false, at(2)), ReturnedToNormal)
	wantState(t, a, RTNUnacked)
	mustAck(t, a, at(3))
	wantState(t, a, Normal)

	a.SetCondition(true, at(4))
	mustAck(t, a, at(5))
	wantState(t, a, ActiveAcked)
	a.SetCondition(false, at(6))
	wantState(t, a, Normal)

	s := a.Status()
	if s.Count != 2 || !s.InAlarmTime.Equal(at(4)) || !s.AckTime.Equal(at(5)) || !s.RTNTime.Equal(at(6)) {
		t.Fatalf("status = %+v", s)
	}
}

func TestAckRequiredLatched(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, Latched: true})
	a.SetCondition(true, at(1))
	if _, err := a.Reset("op", at(2)); !errors.Is(err, ErrNotResettable) {
		t.Fatalf("reset while active: %v", err)
	}
	a.SetCondition(false, at(3))
	wantState(t, a, LatchedUnacked)
	if _, err := a.Reset("op", at(4)); !errors.Is(err, ErrNotResettable) {
		t.Fatalf("reset before ack: %v", err)
	}
	mustAck(t, a, at(5))
	wantState(t, a, LatchedAcked)
	events, err := a.Reset("op", at(6))
	if err != nil {
		t.Fatal(err)
	}
	wantKinds(t, events, Reset)
	wantState(t, a, Normal)
}

func TestNoAckNotLatched(t *testing.T) {
	a := newAlarm(t, Config{})
	a.SetCondition(true, at(1))
	wantState(t, a, ActiveAcked)
	if _, err := a.Ack("op", at(2)); !errors.Is(err, ErrNotUnacked) {
		t.Fatalf("ack: %v", err)
	}
	a.SetCondition(false, at(3))
	wantState(t, a, Normal)
}

func TestNoAckLatched(t *testing.T) {
	a := newAlarm(t, Config{Latched: true})
	a.SetCondition(true, at(1))
	a.SetCondition(false, at(2))
	wantState(t, a, LatchedAcked)
	if !a.Status().InAlarm() {
		t.Fatal("a latched alarm stays in alarm until reset")
	}
	if _, err := a.Reset("op", at(3)); err != nil {
		t.Fatal(err)
	}
	wantState(t, a, Normal)
}

func TestReactivationFromRTN(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, Latched: true})
	a.SetCondition(true, at(1))
	a.SetCondition(false, at(2))
	wantKinds(t, a.SetCondition(true, at(3)), Activated)
	wantState(t, a, ActiveUnacked)
	if a.Status().Count != 2 {
		t.Fatal("re-activation counts as a new activation")
	}
}

func TestShelveNotPermitted(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true})
	if _, err := a.Shelve("op", time.Hour, false, at(1)); !errors.Is(err, ErrShelveNotPermitted) {
		t.Fatalf("err = %v", err)
	}
}

func TestShelveExpiresAndReannunciates(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, MaxShelve: time.Hour})
	a.SetCondition(true, at(0))
	mustAck(t, a, at(1))

	events, err := a.Shelve("op", 2*time.Hour, false, at(10)) // capped at MaxShelve
	if err != nil {
		t.Fatal(err)
	}
	wantKinds(t, events, ShelvedEvent)
	wantState(t, a, Shelved)
	if s := a.Status(); !s.ShelveExpiry.Equal(at(10).Add(time.Hour)) || s.InAlarm() || s.Unacked() {
		t.Fatalf("status = %+v", s)
	}
	if _, err := a.Ack("op", at(11)); !errors.Is(err, ErrNotUnacked) {
		t.Fatalf("ack while shelved: %v", err)
	}

	if events := a.Tick(at(10).Add(time.Hour - time.Second)); len(events) != 0 {
		t.Fatalf("early tick: %v", events)
	}
	events = a.Tick(at(10).Add(time.Hour))
	wantKinds(t, events, UnshelvedEvent, Activated)
	if events[0].Detail != "expired" {
		t.Fatalf("detail = %q", events[0].Detail)
	}
	wantState(t, a, ActiveUnacked)
}

func TestShelvedConditionClearsReturnsToNormal(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, Latched: true, MaxShelve: time.Hour})
	a.SetCondition(true, at(0))
	if _, err := a.Shelve("op", 0, false, at(1)); err != nil {
		t.Fatal(err)
	}
	if events := a.SetCondition(false, at(2)); len(events) != 0 {
		t.Fatalf("a shelved alarm is not annunciated: %v", events)
	}
	wantKinds(t, a.Unshelve("op", at(3)), UnshelvedEvent)
	wantState(t, a, Normal)
}

func TestOneShotShelve(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, MaxShelve: time.Hour})
	if _, err := a.Shelve("op", 0, true, at(0)); !errors.Is(err, ErrNotActive) {
		t.Fatalf("one-shot on an inactive alarm: %v", err)
	}
	a.SetCondition(true, at(1))
	if _, err := a.Shelve("op", 0, true, at(2)); err != nil {
		t.Fatal(err)
	}
	wantKinds(t, a.SetCondition(false, at(3)), UnshelvedEvent)
	wantState(t, a, Normal)
	wantKinds(t, a.SetCondition(true, at(4)), Activated)
}

func TestSuppressionReannunciates(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true})
	a.SetCondition(true, at(0))
	mustAck(t, a, at(1))
	wantKinds(t, a.Suppress("logic", at(2)), SuppressedEvent)
	wantState(t, a, Suppressed)
	if a.Suppress("logic", at(3)) != nil {
		t.Fatal("suppressing twice is a no-op")
	}
	wantKinds(t, a.Unsuppress("logic", at(4)), UnsuppressedEvent, Activated)
	wantState(t, a, ActiveUnacked)
	if a.Status().Count != 2 {
		t.Fatal("re-annunciation counts as an activation")
	}
}

func TestOutOfServiceEndsShelve(t *testing.T) {
	a := newAlarm(t, Config{AckRequired: true, MaxShelve: time.Hour})
	a.SetCondition(true, at(0))
	if _, err := a.Shelve("op", 0, false, at(1)); err != nil {
		t.Fatal(err)
	}
	events := a.Disable("tech", at(2))
	wantKinds(t, events, UnshelvedEvent, Disabled)
	if events[0].Detail != "out of service" {
		t.Fatalf("detail = %q", events[0].Detail)
	}
	wantState(t, a, OutOfService)
	if _, err := a.Shelve("op", 0, false, at(3)); !errors.Is(err, ErrOutOfService) {
		t.Fatalf("shelve while out of service: %v", err)
	}
	if _, err := a.Ack("op", at(3)); !errors.Is(err, ErrOutOfService) {
		t.Fatalf("ack while out of service: %v", err)
	}
	wantKinds(t, a.Enable("tech", at(4)), Enabled, Activated)
	wantState(t, a, ActiveUnacked)
}

func TestDisplayedStatePrecedence(t *testing.T) {
	a := newAlarm(t, Config{MaxShelve: time.Hour})
	if _, err := a.Shelve("op", 0, false, at(0)); err != nil {
		t.Fatal(err)
	}
	a.Suppress("logic", at(1))
	wantState(t, a, Suppressed)
	a.Disable("tech", at(2))
	wantState(t, a, OutOfService)
}

func TestChattering(t *testing.T) {
	a := newAlarm(t, Config{ChatterCount: 3, ChatterWindow: time.Minute})
	toggle := func(s int) []Event {
		events := a.SetCondition(true, at(s))
		return append(events, a.SetCondition(false, at(s+1))...)
	}
	toggle(0)
	toggle(10)
	events := toggle(20)
	wantKinds(t, events, Activated, ChatterStarted, ReturnedToNormal)
	if !a.Status().Chattering {
		t.Fatal("not chattering")
	}
	if events := a.Tick(at(59)); len(events) != 0 {
		t.Fatalf("window not over yet: %v", events)
	}
	wantKinds(t, a.Tick(at(60)), ChatterEnded)
}

func TestCountReset(t *testing.T) {
	a := newAlarm(t, Config{})
	a.SetCondition(true, at(0))
	wantKinds(t, a.ResetCount("op", at(1)), CountReset)
	if s := a.Status(); s.Count != 0 || !s.CountResetTime.Equal(at(1)) {
		t.Fatalf("status = %+v", s)
	}
}

func TestEventCarriesStatesAndUser(t *testing.T) {
	a := newAlarm(t, Config{ID: "LT200.LL", Description: "Guard weight low", Severity: 900, AckRequired: true})
	a.SetCondition(true, at(0))
	events, _ := a.Ack("franklin", at(1))
	e := events[0]
	if e.Alarm != "LT200.LL" || e.Description != "Guard weight low" || e.Previous != ActiveUnacked ||
		e.State != ActiveAcked || e.User != "franklin" || e.Priority != Urgent || !e.Time.Equal(at(1)) {
		t.Fatalf("event = %+v", e)
	}
}

func TestConfigValidate(t *testing.T) {
	bad := []Config{
		{Severity: 500},
		{ID: "A", Severity: 0},
		{ID: "A", Severity: 1001},
		{ID: "A", Severity: 500, MaxShelve: -1},
		{ID: "A", Severity: 500, ChatterCount: -1},
		{ID: "A", Severity: 500, ChatterCount: 3},
	}
	for _, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) accepted an invalid config", cfg)
		}
	}
}

func TestPriorityFromSeverity(t *testing.T) {
	for severity, want := range map[int]Priority{1: Low, 250: Low, 251: Medium, 500: Medium, 501: High, 750: High, 751: Urgent, 1000: Urgent} {
		if got := PriorityFromSeverity(severity); got != want {
			t.Errorf("PriorityFromSeverity(%d) = %v, want %v", severity, got, want)
		}
	}
}

func TestStringers(t *testing.T) {
	if Shelved.String() != "SHELVED" || Shelved.ISA() != "F" || LatchedAcked.ISA() != "E" {
		t.Fatal("state names")
	}
	k, err := ParseEventKind(ChatterStarted.String())
	if err != nil || k != ChatterStarted {
		t.Fatalf("ParseEventKind = %v, %v", k, err)
	}
}

func TestNextDeadline(t *testing.T) {
	a := newAlarm(t, Config{MaxShelve: time.Hour, ChatterCount: 2, ChatterWindow: time.Minute})
	if _, ok := a.NextDeadline(); ok {
		t.Fatal("deadline with nothing pending")
	}
	if _, err := a.Shelve("op", 10*time.Minute, false, at(0)); err != nil {
		t.Fatal(err)
	}
	if d, ok := a.NextDeadline(); !ok || !d.Equal(at(600)) {
		t.Fatalf("shelve deadline = %v, %v", d, ok)
	}
	a.SetCondition(true, at(1))
	a.SetCondition(false, at(2))
	a.SetCondition(true, at(3)) // second rise: chattering until at(1)+1m
	if d, ok := a.NextDeadline(); !ok || !d.Equal(at(61)) {
		t.Fatalf("chatter deadline = %v, %v", d, ok)
	}
}
