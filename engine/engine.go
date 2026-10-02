/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package engine runs a set of alarms: each alarm's evaluator turns the
// samples of its source into a condition, the alarm applies the ISA-18.2 state
// model, and the resulting events go to the sinks (journal, console, tag
// bridge). The engine knows nothing about where samples come from.
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/evaluator"
)

// ErrUnknownAlarm is returned by commands that name an alarm the engine does not have.
var ErrUnknownAlarm = errors.New("engine: unknown alarm")

// Definition is one alarm and the condition that drives it.
type Definition struct {
	Alarm     alarm.Config
	Source    string // name of the value the condition is evaluated on, e.g. a tag
	Condition evaluator.Config
}

// Sink receives events in the order they happened. Publish is called from the
// goroutine that caused the events, never concurrently with itself. A sink may
// read the engine (Status, Statuses, Active) but must not issue commands.
type Sink interface {
	Publish(ctx context.Context, events []alarm.Event) error
}

// SinkFunc adapts a function to a Sink.
type SinkFunc func(ctx context.Context, events []alarm.Event) error

// Publish implements Sink.
func (f SinkFunc) Publish(ctx context.Context, events []alarm.Event) error { return f(ctx, events) }

// Option configures an Engine.
type Option func(*Engine)

// WithSink adds a sink. Sinks are called in the order they were added.
func WithSink(s Sink) Option { return func(e *Engine) { e.sinks = append(e.sinks, s) } }

// WithErrorHandler is called with errors returned by sinks. The default drops them.
func WithErrorHandler(fn func(error)) Option { return func(e *Engine) { e.onError = fn } }

// WithClock replaces time.Now, for tests and simulations.
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// WithSourceTime evaluates delays and rates in device time: a sample whose
// Time is within maxSkew of the engine's clock is evaluated at that time
// instead of when it arrives. Changes that arrive in a burst, e.g. from a
// change feed, are then timed as they happened: a 50 ms pulse does not satisfy
// a 2 s on-delay because it arrived late. Samples with no time, or a time
// further than maxSkew from the clock (a device clock that is wrong), use the
// engine's clock. Evaluation time never goes backwards for an alarm.
func WithSourceTime(maxSkew time.Duration) Option {
	return func(e *Engine) { e.maxSkew = maxSkew }
}

type entry struct {
	def    Definition
	alarm  *alarm.Alarm
	eval   *evaluator.Evaluator
	evalAt time.Time // last evaluation time; evaluation never goes back in time
}

// at returns the time to evaluate a sample at.
func (en *entry) at(s evaluator.Sample, now time.Time, maxSkew time.Duration) time.Time {
	t := now
	if maxSkew > 0 && !s.Time.IsZero() && s.Time.Sub(now).Abs() <= maxSkew {
		t = s.Time
	}
	if t.Before(en.evalAt) {
		t = en.evalAt
	}
	en.evalAt = t
	return t
}

// nextDeadline returns the earliest time the entry's evaluator or alarm has
// work to do.
func (en *entry) nextDeadline() (time.Time, bool) {
	d, ok := en.eval.NextDeadline()
	if ad, aok := en.alarm.NextDeadline(); aok && (!ok || ad.Before(d)) {
		d, ok = ad, true
	}
	return d, ok
}

// setCondition passes the evaluator's condition to the alarm and stamps the
// resulting events with the time of the sample that caused them.
func (en *entry) setCondition(now time.Time) []alarm.Event {
	events := en.alarm.SetCondition(en.eval.Active(), now)
	for i := range events {
		events[i].SourceTime = en.eval.Last().Time
	}
	return events
}

// Engine is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex // guards the alarms, evaluators and queue
	pubMu    sync.Mutex // one goroutine publishes at a time; never acquired while holding mu
	queue    []alarm.Event
	entries  map[string]*entry
	order    []*entry
	bySource map[string][]*entry

	sinks   []Sink
	onError func(error)
	now     func() time.Time
	maxSkew time.Duration
	wake    chan struct{} // tells Run that a deadline may have moved earlier
}

// New returns an engine for the definitions. Alarm IDs must be unique.
func New(defs []Definition, opts ...Option) (*Engine, error) {
	e := &Engine{
		entries:  make(map[string]*entry, len(defs)),
		bySource: make(map[string][]*entry),
		onError:  func(error) {},
		now:      time.Now,
		wake:     make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(e)
	}
	for _, def := range defs {
		if def.Source == "" {
			return nil, fmt.Errorf("engine: alarm %q has no source", def.Alarm.ID)
		}
		if _, dup := e.entries[def.Alarm.ID]; dup {
			return nil, fmt.Errorf("engine: duplicate alarm %q", def.Alarm.ID)
		}
		a, err := alarm.New(def.Alarm)
		if err != nil {
			return nil, err
		}
		ev, err := evaluator.New(def.Condition)
		if err != nil {
			return nil, fmt.Errorf("alarm %s: %w", def.Alarm.ID, err)
		}
		en := &entry{def: def, alarm: a, eval: ev}
		e.entries[def.Alarm.ID] = en
		e.order = append(e.order, en)
		e.bySource[def.Source] = append(e.bySource[def.Source], en)
	}
	return e, nil
}

// Sources returns the names of the values the alarms are evaluated on, sorted.
func (e *Engine) Sources() []string {
	sources := make([]string, 0, len(e.bySource))
	for s := range e.bySource {
		sources = append(sources, s)
	}
	slices.Sort(sources)
	return sources
}

// Definitions returns the alarm definitions in the order they were given.
func (e *Engine) Definitions() []Definition {
	defs := make([]Definition, len(e.order))
	for i, en := range e.order {
		defs[i] = en.def
	}
	return defs
}

// Process evaluates a new sample of source against every alarm on it. Samples
// of one source must be processed in the order they happened.
func (e *Engine) Process(source string, s evaluator.Sample) {
	_ = e.run(func(now time.Time) ([]alarm.Event, error) {
		var events []alarm.Event
		for _, en := range e.bySource[source] {
			if en.eval.Update(s, en.at(s, now, e.maxSkew)) {
				events = append(events, en.setCondition(now)...)
			}
		}
		return events, nil
	})
}

// Tick expires delays and shelves and samples rates of change. Run calls it
// when the next deadline is due; call it yourself when not using Run.
func (e *Engine) Tick() {
	_ = e.run(func(now time.Time) ([]alarm.Event, error) {
		var events []alarm.Event
		for _, en := range e.order {
			if en.eval.Tick(now) {
				events = append(events, en.setCondition(now)...)
			}
			if now.After(en.evalAt) {
				en.evalAt = now
			}
			events = append(events, en.alarm.Tick(now)...)
		}
		return events, nil
	})
}

// NextDeadline returns the earliest time Tick has work to do: a delay expires,
// a rate is due, a shelve expires or chattering ends. ok is false when nothing
// is pending.
func (e *Engine) NextDeadline() (deadline time.Time, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, en := range e.order {
		if d, dok := en.nextDeadline(); dok && (!ok || d.Before(deadline)) {
			deadline, ok = d, true
		}
	}
	return deadline, ok
}

// Run calls Tick at each deadline (see NextDeadline) until ctx is done, so a
// delay or shelve ends on time rather than at the next fixed tick. It wakes
// at least every maxInterval as a safety net, and recomputes the deadline
// whenever a sample or command may have created an earlier one.
func (e *Engine) Run(ctx context.Context, maxInterval time.Duration) error {
	timer := time.NewTimer(maxInterval)
	defer timer.Stop()
	for {
		wait := maxInterval
		if d, ok := e.NextDeadline(); ok {
			wait = min(wait, max(d.Sub(e.now()), time.Millisecond))
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.wake:
			continue // recompute the deadline
		case <-timer.C:
			e.Tick()
		}
	}
}

// Ack acknowledges an alarm.
func (e *Engine) Ack(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Ack(user, now) })
}

// Reset resets a latched alarm.
func (e *Engine) Reset(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Reset(user, now) })
}

// Shelve shelves an alarm; see alarm.Alarm.Shelve.
func (e *Engine) Shelve(id, user string, d time.Duration, oneShot bool) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) {
		return a.Shelve(user, d, oneShot, now)
	})
}

// Unshelve ends a shelve.
func (e *Engine) Unshelve(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Unshelve(user, now), nil })
}

// Suppress suppresses an alarm by design.
func (e *Engine) Suppress(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Suppress(user, now), nil })
}

// Unsuppress ends suppression.
func (e *Engine) Unsuppress(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Unsuppress(user, now), nil })
}

// Disable takes an alarm out of service.
func (e *Engine) Disable(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Disable(user, now), nil })
}

// Enable returns an alarm to service.
func (e *Engine) Enable(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.Enable(user, now), nil })
}

// ResetCount sets an alarm's activation count to zero.
func (e *Engine) ResetCount(id, user string) error {
	return e.command(id, func(a *alarm.Alarm, now time.Time) ([]alarm.Event, error) { return a.ResetCount(user, now), nil })
}

// Status returns a snapshot of one alarm.
func (e *Engine) Status(id string) (alarm.Status, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.entries[id]
	if !ok {
		return alarm.Status{}, false
	}
	return en.alarm.Status(), true
}

// Statuses returns a snapshot of every alarm in definition order.
func (e *Engine) Statuses() []alarm.Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]alarm.Status, len(e.order))
	for i, en := range e.order {
		out[i] = en.alarm.Status()
	}
	return out
}

// Active returns the alarms an operator must see: in alarm or waiting for an
// acknowledgement. They are sorted by priority, then newest first.
func (e *Engine) Active() []alarm.Status {
	var out []alarm.Status
	for _, s := range e.Statuses() {
		if s.InAlarm() || s.Unacked() {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b alarm.Status) int {
		if a.Priority != b.Priority {
			return int(a.Priority) - int(b.Priority)
		}
		return b.InAlarmTime.Compare(a.InAlarmTime)
	})
	return out
}

func (e *Engine) command(id string, fn func(*alarm.Alarm, time.Time) ([]alarm.Event, error)) error {
	return e.run(func(now time.Time) ([]alarm.Event, error) {
		en, ok := e.entries[id]
		if !ok {
			return nil, fmt.Errorf("%w %q", ErrUnknownAlarm, id)
		}
		events, err := fn(en.alarm, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		return events, nil
	})
}

// run calls fn under the lock, queues its events and publishes them before
// returning. Events are queued under mu, so the queue is in the order they
// happened; publishing takes pubMu and then mu only briefly to take the queue,
// and never the other way round, so sinks can read the engine.
func (e *Engine) run(fn func(now time.Time) ([]alarm.Event, error)) error {
	e.mu.Lock()
	events, err := fn(e.now())
	e.queue = append(e.queue, events...)
	e.mu.Unlock()
	select { // a sample or command may have started an earlier deadline
	case e.wake <- struct{}{}:
	default:
	}
	if len(events) > 0 {
		e.flush()
	}
	return err
}

// flush publishes queued events until the queue is empty. A goroutine that
// finds another one publishing waits for it; its events are then either
// already published or still queued for it to publish.
func (e *Engine) flush() {
	e.pubMu.Lock()
	defer e.pubMu.Unlock()
	ctx := context.Background()
	for {
		e.mu.Lock()
		batch := e.queue
		e.queue = nil
		e.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		for _, s := range e.sinks {
			if err := s.Publish(ctx, batch); err != nil {
				e.onError(err)
			}
		}
	}
}
