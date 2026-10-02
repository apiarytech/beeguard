/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package slogsink writes alarm events to a log/slog logger, so beeguard needs
// no third-party logging library. Use slog.NewTextHandler for people and
// slog.NewJSONHandler for log collectors.
//
// The level carries the urgency: an activation logs at ERROR for urgent and
// high priority alarms, WARN for medium and INFO for low. Taking an alarm out
// of view (shelve, suppress, out of service) and chattering log at WARN, and
// everything else at INFO.
package slogsink

import (
	"context"
	"log/slog"
	"time"

	"github.com/apiarytech/beeguard/alarm"
)

// Sink is an engine.Sink that logs every event.
type Sink struct {
	logger *slog.Logger
}

// New returns a sink that writes to logger.
func New(logger *slog.Logger) *Sink { return &Sink{logger: logger} }

// Level returns the level an event is logged at.
func Level(e alarm.Event) slog.Level {
	switch e.Kind {
	case alarm.Activated:
		switch e.Priority {
		case alarm.Urgent, alarm.High:
			return slog.LevelError
		case alarm.Medium:
			return slog.LevelWarn
		}
		return slog.LevelInfo
	case alarm.ShelvedEvent, alarm.SuppressedEvent, alarm.Disabled, alarm.ChatterStarted:
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// Publish implements engine.Sink.
func (s *Sink) Publish(ctx context.Context, events []alarm.Event) error {
	for _, e := range events {
		attrs := []slog.Attr{
			slog.String("alarm", e.Alarm),
			slog.String("prio", e.Priority.String()),
			slog.String("state", e.State.String()),
		}
		if e.Description != "" {
			attrs = append(attrs, slog.String("desc", e.Description))
		}
		if e.User != "" {
			attrs = append(attrs, slog.String("user", e.User))
		}
		if e.Detail != "" {
			attrs = append(attrs, slog.String("detail", e.Detail))
		}
		if !e.SourceTime.IsZero() {
			attrs = append(attrs, slog.String("source_time", e.SourceTime.Format(time.RFC3339Nano)))
		}
		s.logger.LogAttrs(ctx, Level(e), e.Kind.String(), attrs...)
	}
	return nil
}
