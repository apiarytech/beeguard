/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package journal records alarm events: the sequence-of-events record ISA-18.2
// expects for alarm analysis and audit. A Journal is an engine.Sink, so the
// engine writes every event to it in order.
package journal

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/apiarytech/beeguard/alarm"
)

// Filter selects events. Zero fields do not filter.
type Filter struct {
	Alarm string
	Kinds []alarm.EventKind
	Since time.Time // inclusive
	Until time.Time // exclusive
	Limit int       // keep only the most recent Limit matches (the oldest, with Oldest)
	// Oldest counts Offset and Limit from the oldest match instead of the
	// most recent, so a long range (a day, for a report) is read in pages:
	// Offset 0, Limit n; then Offset n; and so on.
	Oldest bool
	// Offset skips this many matches first: the most recent ones, or the
	// oldest with Oldest. The result is oldest first either way.
	Offset int
}

// Window applies Offset, Limit and Oldest to matches, oldest first.
func (f Filter) Window(matches []alarm.Event) []alarm.Event {
	n := len(matches)
	off := min(max(f.Offset, 0), n)
	if f.Oldest {
		matches = matches[off:]
		if f.Limit > 0 && len(matches) > f.Limit {
			matches = matches[:f.Limit]
		}
		return matches
	}
	matches = matches[:n-off]
	if f.Limit > 0 && len(matches) > f.Limit {
		matches = matches[len(matches)-f.Limit:]
	}
	return matches
}

// Match reports whether e passes the filter, ignoring Limit, Oldest and
// Offset.
func (f Filter) Match(e alarm.Event) bool {
	switch {
	case f.Alarm != "" && e.Alarm != f.Alarm:
		return false
	case len(f.Kinds) > 0 && !slices.Contains(f.Kinds, e.Kind):
		return false
	case !f.Since.IsZero() && e.Time.Before(f.Since):
		return false
	case !f.Until.IsZero() && !e.Time.Before(f.Until):
		return false
	}
	return true
}

// Journal stores events and returns them oldest first.
type Journal interface {
	Publish(ctx context.Context, events []alarm.Event) error
	Query(ctx context.Context, f Filter) ([]alarm.Event, error)
}

// Purger is a Journal that can drop old events, for retention.
type Purger interface {
	// Purge deletes the events recorded before before and returns how many.
	Purge(ctx context.Context, before time.Time) (int64, error)
}

// Memory is a Journal that keeps the most recent events in memory.
type Memory struct {
	mu     sync.Mutex
	max    int
	events []alarm.Event
}

// NewMemory returns a journal that keeps at most max events; max <= 0 means no limit.
func NewMemory(maxEvents int) *Memory { return &Memory{max: maxEvents} }

// Publish implements engine.Sink.
func (m *Memory) Publish(_ context.Context, events []alarm.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	if m.max > 0 && len(m.events) > m.max {
		m.events = slices.Clone(m.events[len(m.events)-m.max:])
	}
	return nil
}

// Query returns the matching events, oldest first.
func (m *Memory) Query(_ context.Context, f Filter) ([]alarm.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []alarm.Event
	for _, e := range m.events {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return f.Window(out), nil
}

// Purge drops the events recorded before before.
func (m *Memory) Purge(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.events)
	m.events = slices.DeleteFunc(m.events, func(e alarm.Event) bool { return e.Time.Before(before) })
	return int64(n - len(m.events)), nil
}

var _ Purger = (*Memory)(nil)

// Retain deletes the events older than retention from p, at once and then
// every interval (an hour if zero), until ctx ends. A zero retention keeps
// everything: it returns at once. Errors go to onError, if set.
func Retain(ctx context.Context, p Purger, retention, every time.Duration, onError func(error)) {
	if retention <= 0 {
		return
	}
	if every <= 0 {
		every = time.Hour
	}
	purge := func() {
		if _, err := p.Purge(ctx, time.Now().Add(-retention)); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
	}
	purge()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}
