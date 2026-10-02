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
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/evaluator"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type recorder struct {
	mu     sync.Mutex
	events []alarm.Event
}

func (r *recorder) Publish(_ context.Context, events []alarm.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, events...)
	return nil
}

func (r *recorder) kinds() []alarm.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	kinds := make([]alarm.EventKind, len(r.events))
	for i, e := range r.events {
		kinds[i] = e.Kind
	}
	return kinds
}

func guardDefs() []Definition {
	return []Definition{
		{
			Alarm:     alarm.Config{ID: "Guard1.TempHigh", Description: "Guard 1 temperature high", Severity: 600, AckRequired: true, MaxShelve: time.Hour},
			Source:    "Guard1.Temp",
			Condition: evaluator.Config{Kind: evaluator.High, Limit: 38, Deadband: 1, OnDelay: 5 * time.Second},
		},
		{
			Alarm:     alarm.Config{ID: "Guard1.TempHighHigh", Severity: 900, AckRequired: true, Latched: true},
			Source:    "Guard1.Temp",
			Condition: evaluator.Config{Kind: evaluator.High, Limit: 40},
		},
		{
			Alarm:     alarm.Config{ID: "Guard1.LidOpen", Severity: 300, AckRequired: true},
			Source:    "Guard1.Lid",
			Condition: evaluator.Config{Kind: evaluator.Digital},
		},
	}
}

func newEngine(t *testing.T) (*Engine, *clock, *recorder) {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)}
	r := &recorder{}
	e, err := New(guardDefs(), WithClock(c.Now), WithSink(r))
	if err != nil {
		t.Fatal(err)
	}
	return e, c, r
}

func sample(v float64, ts time.Time) evaluator.Sample {
	return evaluator.Sample{Value: v, Good: true, Time: ts}
}

func TestSources(t *testing.T) {
	e, _, _ := newEngine(t)
	if got := e.Sources(); len(got) != 2 || got[0] != "Guard1.Lid" || got[1] != "Guard1.Temp" {
		t.Fatalf("sources = %v", got)
	}
}

func TestProcessWithOnDelayAndSourceTime(t *testing.T) {
	e, c, r := newEngine(t)
	deviceTime := c.Now().Add(-200 * time.Millisecond)
	e.Process("Guard1.Temp", sample(39, deviceTime))
	if len(r.kinds()) != 0 {
		t.Fatalf("events before the on-delay: %v", r.events)
	}
	c.Advance(5 * time.Second)
	e.Tick()
	if len(r.events) != 1 || r.events[0].Kind != alarm.Activated || r.events[0].Alarm != "Guard1.TempHigh" {
		t.Fatalf("events = %v", r.events)
	}
	if !r.events[0].SourceTime.Equal(deviceTime) || !r.events[0].Time.Equal(c.Now()) {
		t.Fatalf("times = %v / %v", r.events[0].SourceTime, r.events[0].Time)
	}
}

func TestOneSourceDrivesSeveralAlarms(t *testing.T) {
	e, c, r := newEngine(t)
	e.Process("Guard1.Temp", sample(41, c.Now()))
	if s, _ := e.Status("Guard1.TempHighHigh"); s.State != alarm.ActiveUnacked {
		t.Fatalf("HH state = %v", s.State)
	}
	if s, _ := e.Status("Guard1.TempHigh"); s.State != alarm.Normal {
		t.Fatalf("H must wait for its on-delay, state = %v", s.State)
	}
	c.Advance(5 * time.Second)
	e.Tick()
	if got := len(r.kinds()); got != 2 {
		t.Fatalf("events = %v", r.events)
	}
}

func TestCommands(t *testing.T) {
	e, c, r := newEngine(t)
	e.Process("Guard1.Lid", sample(1, c.Now()))
	if err := e.Ack("Guard1.LidOpen", "op"); err != nil {
		t.Fatal(err)
	}
	if err := e.Ack("Guard1.LidOpen", "op"); !errors.Is(err, alarm.ErrNotUnacked) {
		t.Fatalf("second ack: %v", err)
	}
	if err := e.Ack("Nope", "op"); !errors.Is(err, ErrUnknownAlarm) {
		t.Fatalf("unknown alarm: %v", err)
	}
	if err := e.Shelve("Guard1.LidOpen", "op", 0, false); !errors.Is(err, alarm.ErrShelveNotPermitted) {
		t.Fatalf("shelve: %v", err)
	}
	for _, cmd := range []func(string, string) error{e.Suppress, e.Unsuppress, e.Disable, e.Enable, e.ResetCount} {
		if err := cmd("Guard1.LidOpen", "op"); err != nil {
			t.Fatal(err)
		}
	}
	want := []alarm.EventKind{alarm.Activated, alarm.Acknowledged, alarm.SuppressedEvent, alarm.UnsuppressedEvent,
		alarm.Activated, alarm.Disabled, alarm.Enabled, alarm.Activated, alarm.CountReset}
	got := r.kinds()
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestShelveExpiresOnTick(t *testing.T) {
	e, c, _ := newEngine(t)
	if err := e.Shelve("Guard1.TempHigh", "op", 10*time.Minute, false); err != nil {
		t.Fatal(err)
	}
	c.Advance(10 * time.Minute)
	e.Tick()
	if s, _ := e.Status("Guard1.TempHigh"); s.Shelved {
		t.Fatal("shelve did not expire")
	}
}

func TestLatchedResetAndUnshelve(t *testing.T) {
	e, c, _ := newEngine(t)
	e.Process("Guard1.Temp", sample(41, c.Now()))
	e.Process("Guard1.Temp", sample(30, c.Now()))
	if err := e.Reset("Guard1.TempHighHigh", "op"); !errors.Is(err, alarm.ErrNotResettable) {
		t.Fatalf("reset before ack: %v", err)
	}
	if err := e.Ack("Guard1.TempHighHigh", "op"); err != nil {
		t.Fatal(err)
	}
	if err := e.Reset("Guard1.TempHighHigh", "op"); err != nil {
		t.Fatal(err)
	}
	if err := e.Unshelve("Guard1.TempHigh", "op"); err != nil {
		t.Fatalf("unshelving an unshelved alarm is a no-op: %v", err)
	}
}

func TestActiveOrder(t *testing.T) {
	e, c, _ := newEngine(t)
	e.Process("Guard1.Lid", sample(1, c.Now())) // severity 300: medium
	c.Advance(time.Second)
	e.Process("Guard1.Temp", sample(41, c.Now())) // HH severity 900: urgent
	active := e.Active()
	if len(active) != 2 || active[0].ID != "Guard1.TempHighHigh" || active[1].ID != "Guard1.LidOpen" {
		t.Fatalf("active = %+v", active)
	}
}

// A sink may read the engine while it publishes.
func TestSinkCanReadEngine(t *testing.T) {
	var e *Engine
	var seen alarm.State
	sink := SinkFunc(func(_ context.Context, events []alarm.Event) error {
		s, _ := e.Status(events[0].Alarm)
		seen = s.State
		return nil
	})
	var err error
	e, err = New(guardDefs(), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	e.Process("Guard1.Lid", sample(1, time.Now()))
	if seen != alarm.ActiveUnacked {
		t.Fatalf("sink saw %v", seen)
	}
}

// Regression: a sink that reads the engine while other goroutines process
// samples deadlocked when publishing held pubMu and processing held mu.
func TestConcurrentProcessWithReadingSink(t *testing.T) {
	var e *Engine
	var mu sync.Mutex
	var events []alarm.Event
	sink := SinkFunc(func(_ context.Context, batch []alarm.Event) error {
		for _, ev := range batch {
			_, _ = e.Status(ev.Alarm)
		}
		mu.Lock()
		events = append(events, batch...)
		mu.Unlock()
		return nil
	})
	var err error
	e, err = New(guardDefs(), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := range 200 {
			wg.Go(func() {
				source, v := "Guard1.Lid", float64(i%2)
				if i%3 == 0 {
					source, v = "Guard1.Temp", 30+float64(i%2)*12
				}
				e.Process(source, sample(v, time.Now()))
			})
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock")
	}
	// Each alarm's own events must still alternate between activation and return to normal.
	last := map[string]alarm.EventKind{}
	for _, ev := range events {
		if ev.Kind == last[ev.Alarm] {
			t.Fatalf("%s: %v twice in a row", ev.Alarm, ev.Kind)
		}
		last[ev.Alarm] = ev.Kind
	}
}

func TestSinkErrorsReachHandler(t *testing.T) {
	boom := errors.New("boom")
	var got error
	e, err := New(guardDefs(),
		WithSink(SinkFunc(func(context.Context, []alarm.Event) error { return boom })),
		WithErrorHandler(func(err error) { got = err }))
	if err != nil {
		t.Fatal(err)
	}
	e.Process("Guard1.Lid", sample(1, time.Now()))
	if !errors.Is(got, boom) {
		t.Fatalf("handler got %v", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	e, c, r := newEngine(t)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			e.Process("Guard1.Lid", sample(float64(i%2), c.Now()))
			e.Tick()
			_ = e.Active()
		})
	}
	wg.Wait()
	// Activations and returns to normal must alternate however the goroutines interleaved.
	var last alarm.EventKind
	for _, ev := range r.events {
		if ev.Kind == last {
			t.Fatalf("events out of order: %v", r.kinds())
		}
		last = ev.Kind
	}
}

func TestRun(t *testing.T) {
	e, _, _ := newEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- e.Run(ctx, time.Millisecond) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

func TestNewRejectsBadDefinitions(t *testing.T) {
	defs := guardDefs()
	cases := map[string][]Definition{
		"duplicate":  append(guardDefs(), defs[0]),
		"no source":  {{Alarm: alarm.Config{ID: "A", Severity: 500}, Condition: evaluator.Config{Kind: evaluator.Digital}}},
		"bad alarm":  {{Alarm: alarm.Config{ID: "A"}, Source: "S", Condition: evaluator.Config{Kind: evaluator.Digital}}},
		"bad config": {{Alarm: alarm.Config{ID: "A", Severity: 500}, Source: "S"}},
	}
	for name, defs := range cases {
		if _, err := New(defs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	e, _ := New(guardDefs())
	if got := len(e.Definitions()); got != 3 {
		t.Fatalf("definitions = %d", got)
	}
}

// A condition that already lasted longer than its on-delay when its samples
// arrive (e.g. replayed from a change feed) activates at once in device time.
func TestSourceTime(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 1, 8, 0, 10, 0, time.UTC)}
	defs := []Definition{{
		Alarm:     alarm.Config{ID: "Slow", Severity: 500},
		Source:    "S",
		Condition: evaluator.Config{Kind: evaluator.Digital, OnDelay: 2 * time.Second},
	}}
	run := func(opts ...Option) alarm.State {
		e, err := New(defs, append(opts, WithClock(c.Now))...)
		if err != nil {
			t.Fatal(err)
		}
		e.Process("S", sample(1, c.Now().Add(-3*time.Second))) // the change happened 3 s ago
		e.Tick()
		s, _ := e.Status("Slow")
		return s.State
	}
	if got := run(WithSourceTime(5 * time.Second)); got != alarm.ActiveAcked {
		t.Fatalf("with source time: %v", got)
	}
	if got := run(); got != alarm.Normal {
		t.Fatalf("without source time the on-delay starts on arrival: %v", got)
	}

	// A device clock an hour off is ignored.
	e, _ := New(defs, WithClock(c.Now), WithSourceTime(5*time.Second))
	e.Process("S", sample(1, c.Now().Add(-time.Hour)))
	e.Tick()
	if s, _ := e.Status("Slow"); s.State != alarm.Normal {
		t.Fatalf("skewed device time was used: %v", s.State)
	}
	if d, ok := e.NextDeadline(); !ok || !d.Equal(c.Now().Add(2*time.Second)) {
		t.Fatalf("deadline = %v, %v", d, ok)
	}
}

// Evaluation time never goes backwards, even if a sample's device time does.
func TestSourceTimeIsMonotonic(t *testing.T) {
	c := &clock{now: time.Date(2026, 10, 1, 8, 0, 10, 0, time.UTC)}
	e, _ := New([]Definition{{
		Alarm:     alarm.Config{ID: "A", Severity: 500},
		Source:    "S",
		Condition: evaluator.Config{Kind: evaluator.Digital, OnDelay: time.Second},
	}}, WithClock(c.Now), WithSourceTime(5*time.Second))
	e.Process("S", sample(1, c.Now()))
	e.Process("S", sample(1, c.Now().Add(-2*time.Second))) // older device time
	if d, _ := e.NextDeadline(); !d.Equal(c.Now().Add(time.Second)) {
		t.Fatalf("deadline moved back to %v", d)
	}
}

// Run wakes at the deadline, not at its safety interval.
func TestRunWakesAtDeadline(t *testing.T) {
	e, err := New([]Definition{{
		Alarm:     alarm.Config{ID: "A", Severity: 500},
		Source:    "S",
		Condition: evaluator.Config{Kind: evaluator.Digital, OnDelay: 50 * time.Millisecond},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = e.Run(ctx, 10*time.Second) }()
	time.Sleep(10 * time.Millisecond) // Run is now waiting with no deadline
	began := time.Now()
	e.Process("S", sample(1, time.Now()))
	for {
		if s, _ := e.Status("A"); s.InAlarm() {
			break
		}
		if time.Since(began) > 2*time.Second {
			t.Fatal("the on-delay did not fire at its deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if took := time.Since(began); took < 50*time.Millisecond {
		t.Fatalf("fired after %v, before the on-delay", took)
	}
}
