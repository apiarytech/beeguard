/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package api is beeguard's service interface, independent of any transport.
//
// Service is what other systems use: list and read alarms, send operator
// commands, and query the event journal. Requests and responses are plain
// structs with JSON tags, and errors carry a Code, so the same Service can be
// exposed over HTTP (package httpapi), gRPC, or as a go-micro service handler
// without changing beeguard.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/journal"
)

// Code classifies an error so a transport can map it, e.g. to an HTTP status.
type Code string

const (
	CodeNotFound        Code = "not_found"           // no such alarm
	CodeInvalid         Code = "invalid_argument"    // malformed request
	CodeRejected        Code = "failed_precondition" // not allowed in the alarm's current state
	CodeUnavailable     Code = "unavailable"         // a dependency, e.g. the journal, failed
	CodeUnauthenticated Code = "unauthenticated"     // missing or wrong credentials
	CodeInternal        Code = "internal"
)

// Error is an error with a Code.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func errorf(code Code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the Code of err, or CodeInternal.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// Alarm is an alarm's current status.
type Alarm struct {
	ID          string     `json:"id"`
	Description string     `json:"description,omitempty"`
	State       string     `json:"state"`      // e.g. ACTIVE_UNACK
	StateCode   int        `json:"state_code"` // 0 NORMAL .. 8 OUT_OF_SERVICE
	ISA         string     `json:"isa"`        // ISA-18.2 state letter A..H
	InAlarm     bool       `json:"in_alarm"`
	Acked       bool       `json:"acked"`
	Condition   bool       `json:"condition"`
	Shelved     bool       `json:"shelved"`
	Suppressed  bool       `json:"suppressed"`
	Disabled    bool       `json:"disabled"`
	Chattering  bool       `json:"chattering"`
	Priority    int        `json:"priority"` // 1 urgent .. 4 low
	Severity    int        `json:"severity"`
	Count       int        `json:"count"`
	InAlarmTime *time.Time `json:"in_alarm_time,omitempty"`
	AckTime     *time.Time `json:"ack_time,omitempty"`
	RTNTime     *time.Time `json:"rtn_time,omitempty"`
	ShelveUntil *time.Time `json:"shelve_until,omitempty"`
}

// Event is one journal entry.
type Event struct {
	Time        time.Time  `json:"time"`
	SourceTime  *time.Time `json:"source_time,omitempty"`
	Alarm       string     `json:"alarm"`
	Description string     `json:"description,omitempty"`
	Kind        string     `json:"kind"`
	State       string     `json:"state"`
	Previous    string     `json:"previous"`
	Priority    int        `json:"priority"`
	User        string     `json:"user,omitempty"`
	Detail      string     `json:"detail,omitempty"`
}

// Command is an operator command.
type Command string

const (
	CmdAck        Command = "ack"
	CmdReset      Command = "reset"
	CmdShelve     Command = "shelve"
	CmdUnshelve   Command = "unshelve"
	CmdSuppress   Command = "suppress"
	CmdUnsuppress Command = "unsuppress"
	CmdDisable    Command = "disable"
	CmdEnable     Command = "enable"
	CmdResetCount Command = "reset-count"
)

// CommandRequest asks for a command on one alarm.
type CommandRequest struct {
	Alarm         string  `json:"alarm"`
	Command       Command `json:"command"`
	User          string  `json:"user"`                     // required: recorded in the journal
	ShelveMinutes int     `json:"shelve_minutes,omitempty"` // shelve: 0 = the alarm's maximum
	OneShot       bool    `json:"one_shot,omitempty"`       // shelve: end when the condition clears
}

// EventQuery selects journal entries. Zero fields do not filter.
type EventQuery struct {
	Alarm string    `json:"alarm,omitempty"`
	Kinds []string  `json:"kinds,omitempty"` // e.g. ACTIVATED, RTN, SHELVED
	Since time.Time `json:"since,omitzero"`
	Until time.Time `json:"until,omitzero"`
	Limit int       `json:"limit,omitempty"` // most recent entries; 0 = DefaultLimit, capped at MaxLimit
}

// Limits on EventQuery.Limit.
const (
	DefaultLimit = 1000
	MaxLimit     = 10000
)

// Service is beeguard's API.
type Service interface {
	// ListAlarms returns every alarm, in configuration order.
	ListAlarms(ctx context.Context) ([]Alarm, error)
	// ActiveAlarms returns the alarms in alarm or waiting for an
	// acknowledgement, most urgent first, then newest first.
	ActiveAlarms(ctx context.Context) ([]Alarm, error)
	// GetAlarm returns one alarm.
	GetAlarm(ctx context.Context, id string) (Alarm, error)
	// Command runs an operator command and returns the alarm's new status.
	Command(ctx context.Context, req CommandRequest) (Alarm, error)
	// QueryEvents returns journal entries, oldest first.
	QueryEvents(ctx context.Context, q EventQuery) ([]Event, error)
}

// New returns a Service backed by an engine and its journal.
func New(eng *engine.Engine, j journal.Journal) Service { return &service{eng: eng, journal: j} }

type service struct {
	eng     *engine.Engine
	journal journal.Journal
}

func (s *service) ListAlarms(context.Context) ([]Alarm, error) {
	return toAlarms(s.eng.Statuses()), nil
}

func (s *service) ActiveAlarms(context.Context) ([]Alarm, error) {
	return toAlarms(s.eng.Active()), nil
}

func (s *service) GetAlarm(_ context.Context, id string) (Alarm, error) {
	st, ok := s.eng.Status(id)
	if !ok {
		return Alarm{}, errorf(CodeNotFound, "no alarm %q", id)
	}
	return toAlarm(st), nil
}

func (s *service) Command(ctx context.Context, req CommandRequest) (Alarm, error) {
	if req.User == "" {
		return Alarm{}, errorf(CodeInvalid, "user is required")
	}
	if req.ShelveMinutes < 0 {
		return Alarm{}, errorf(CodeInvalid, "shelve_minutes must not be negative")
	}
	var err error
	switch req.Command {
	case CmdAck:
		err = s.eng.Ack(req.Alarm, req.User)
	case CmdReset:
		err = s.eng.Reset(req.Alarm, req.User)
	case CmdShelve:
		err = s.eng.Shelve(req.Alarm, req.User, time.Duration(req.ShelveMinutes)*time.Minute, req.OneShot)
	case CmdUnshelve:
		err = s.eng.Unshelve(req.Alarm, req.User)
	case CmdSuppress:
		err = s.eng.Suppress(req.Alarm, req.User)
	case CmdUnsuppress:
		err = s.eng.Unsuppress(req.Alarm, req.User)
	case CmdDisable:
		err = s.eng.Disable(req.Alarm, req.User)
	case CmdEnable:
		err = s.eng.Enable(req.Alarm, req.User)
	case CmdResetCount:
		err = s.eng.ResetCount(req.Alarm, req.User)
	default:
		return Alarm{}, errorf(CodeInvalid, "unknown command %q", req.Command)
	}
	if err != nil {
		return Alarm{}, commandError(err)
	}
	return s.GetAlarm(ctx, req.Alarm)
}

func commandError(err error) error {
	switch {
	case errors.Is(err, engine.ErrUnknownAlarm):
		return &Error{Code: CodeNotFound, Message: err.Error()}
	case errors.Is(err, alarm.ErrNotUnacked), errors.Is(err, alarm.ErrNotResettable),
		errors.Is(err, alarm.ErrShelveNotPermitted), errors.Is(err, alarm.ErrOutOfService),
		errors.Is(err, alarm.ErrNotActive):
		return &Error{Code: CodeRejected, Message: err.Error()}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

func (s *service) QueryEvents(ctx context.Context, q EventQuery) ([]Event, error) {
	f := journal.Filter{Alarm: q.Alarm, Since: q.Since, Until: q.Until, Limit: q.Limit}
	switch {
	case f.Limit < 0:
		return nil, errorf(CodeInvalid, "limit must not be negative")
	case f.Limit == 0:
		f.Limit = DefaultLimit
	case f.Limit > MaxLimit:
		f.Limit = MaxLimit
	}
	for _, k := range q.Kinds {
		kind, err := alarm.ParseEventKind(k)
		if err != nil {
			return nil, errorf(CodeInvalid, "%v", err)
		}
		f.Kinds = append(f.Kinds, kind)
	}
	events, err := s.journal.Query(ctx, f)
	if err != nil {
		return nil, errorf(CodeUnavailable, "journal: %v", err)
	}
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = Event{
			Time: e.Time, SourceTime: timePtr(e.SourceTime), Alarm: e.Alarm, Description: e.Description,
			Kind: e.Kind.String(), State: e.State.String(), Previous: e.Previous.String(),
			Priority: int(e.Priority), User: e.User, Detail: e.Detail,
		}
	}
	return out, nil
}

func toAlarms(statuses []alarm.Status) []Alarm {
	out := make([]Alarm, len(statuses))
	for i, s := range statuses {
		out[i] = toAlarm(s)
	}
	return out
}

func toAlarm(s alarm.Status) Alarm {
	a := Alarm{
		ID: s.ID, Description: s.Description, State: s.State.String(), StateCode: int(s.State),
		ISA: s.State.ISA(), InAlarm: s.InAlarm(), Acked: s.Acked(), Condition: s.Condition,
		Shelved: s.Shelved, Suppressed: s.Suppressed, Disabled: s.Disabled, Chattering: s.Chattering,
		Priority: int(s.Priority), Severity: s.Severity, Count: s.Count,
		InAlarmTime: timePtr(s.InAlarmTime), AckTime: timePtr(s.AckTime), RTNTime: timePtr(s.RTNTime),
	}
	if s.Shelved {
		a.ShelveUntil = timePtr(s.ShelveExpiry)
	}
	return a
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
