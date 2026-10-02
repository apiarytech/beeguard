/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package slogsink

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
)

func TestLevel(t *testing.T) {
	cases := []struct {
		e    alarm.Event
		want slog.Level
	}{
		{alarm.Event{Kind: alarm.Activated, Priority: alarm.Urgent}, slog.LevelError},
		{alarm.Event{Kind: alarm.Activated, Priority: alarm.High}, slog.LevelError},
		{alarm.Event{Kind: alarm.Activated, Priority: alarm.Medium}, slog.LevelWarn},
		{alarm.Event{Kind: alarm.Activated, Priority: alarm.Low}, slog.LevelInfo},
		{alarm.Event{Kind: alarm.ShelvedEvent}, slog.LevelWarn},
		{alarm.Event{Kind: alarm.Disabled}, slog.LevelWarn},
		{alarm.Event{Kind: alarm.Acknowledged, Priority: alarm.Urgent}, slog.LevelInfo},
	}
	for _, c := range cases {
		if got := Level(c.e); got != c.want {
			t.Errorf("Level(%v %v) = %v, want %v", c.e.Kind, c.e.Priority, got, c.want)
		}
	}
}

func TestPublishJSON(t *testing.T) {
	var buf bytes.Buffer
	s := New(slog.New(slog.NewJSONHandler(&buf, nil)))
	src := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	err := s.Publish(context.Background(), []alarm.Event{{
		Alarm: "Guard1.TempHigh", Description: "Guard 1 temperature high", Kind: alarm.ShelvedEvent,
		State: alarm.Shelved, Priority: alarm.High, User: "franklin", Detail: "for 1h0m0s", SourceTime: src,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	want := map[string]any{
		"level": "WARN", "msg": "SHELVED", "alarm": "Guard1.TempHigh", "prio": "HIGH", "state": "SHELVED",
		"desc": "Guard 1 temperature high", "user": "franklin", "detail": "for 1h0m0s",
		"source_time": "2026-10-01T08:00:00Z",
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}
}

func TestPublishTextOmitsEmpty(t *testing.T) {
	var buf bytes.Buffer
	s := New(slog.New(slog.NewTextHandler(&buf, nil)))
	_ = s.Publish(context.Background(), []alarm.Event{{Alarm: "Guard2.LidOpen", Kind: alarm.Activated, Priority: alarm.Low, State: alarm.ActiveUnacked}})
	out := buf.String()
	if !strings.Contains(out, "msg=ACTIVATED") || !strings.Contains(out, "alarm=Guard2.LidOpen") || strings.Contains(out, "user=") {
		t.Fatalf("output = %q", out)
	}
}
