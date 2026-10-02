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
	Limit int       // keep only the most recent Limit matches
}

// Match reports whether e passes the filter, ignoring Limit.
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
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[len(out)-f.Limit:]
	}
	return out, nil
}
