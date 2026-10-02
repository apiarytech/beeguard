/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package alarm implements the ISA-18.2 / IEC 62682 alarm state model for one
// alarm condition: acknowledge, latching, shelving, suppression by design,
// out of service, chattering detection and alarm counting.
//
// It is the Go equivalent of the BG_ALM_DIG function block in
// reference/digital_alarm.st, minus condition filtering (deadband and on/off
// delays), which lives in the evaluator package. An Alarm is pure: it has no
// clock, goroutines or I/O. Every method takes the current time and returns the
// events it caused, and Tick must be called periodically so shelving expires.
// An Alarm is not safe for concurrent use.
package alarm

import (
	"errors"
	"fmt"
	"time"
)

// Errors returned by the operator commands.
var (
	ErrNotUnacked         = errors.New("alarm: nothing to acknowledge")
	ErrNotResettable      = errors.New("alarm: only a latched alarm that has returned to normal and been acknowledged can be reset")
	ErrShelveNotPermitted = errors.New("alarm: shelving is not permitted for this alarm")
	ErrOutOfService       = errors.New("alarm: alarm is out of service")
	ErrNotActive          = errors.New("alarm: a one-shot shelve needs an active condition")
)

// Config describes one alarm.
type Config struct {
	ID          string
	Description string
	Severity    int  // 1..1000, mapped to a Priority
	AckRequired bool // false: the alarm is acknowledged as soon as it activates
	Latched     bool // true: after the condition clears the alarm stays in alarm until reset

	// MaxShelve is the longest an operator may shelve the alarm. Zero means
	// shelving is not permitted, since ISA-18.2 requires shelving to be time limited.
	MaxShelve time.Duration

	// The alarm is chattering while ChatterCount or more activations of the
	// condition fall within ChatterWindow. ChatterCount 0 turns detection off.
	ChatterCount  int
	ChatterWindow time.Duration
}

// Validate reports a configuration the alarm cannot run with.
func (c Config) Validate() error {
	switch {
	case c.ID == "":
		return errors.New("alarm: ID is required")
	case c.Severity < 1 || c.Severity > 1000:
		return fmt.Errorf("alarm %s: severity %d is outside 1..1000", c.ID, c.Severity)
	case c.MaxShelve < 0:
		return fmt.Errorf("alarm %s: negative MaxShelve", c.ID)
	case c.ChatterCount < 0:
		return fmt.Errorf("alarm %s: negative ChatterCount", c.ID)
	case c.ChatterCount > 0 && c.ChatterWindow <= 0:
		return fmt.Errorf("alarm %s: ChatterCount needs a positive ChatterWindow", c.ID)
	}
	return nil
}

// Alarm is the state of one alarm.
type Alarm struct {
	cfg      Config
	priority Priority

	cond       bool  // condition as last reported
	base       State // Normal .. LatchedAcked; held at Normal while hidden
	shelved    bool
	oneShot    bool
	suppressed bool
	disabled   bool
	chattering bool
	rises      []time.Time // last ChatterCount condition activations, oldest first
	count      int

	inAlarmTime, ackTime, rtnTime, resetTime               time.Time
	shelveTime, unshelveTime, shelveExpiry, countResetTime time.Time
}

// New returns an alarm in the NORMAL state.
func New(cfg Config) (*Alarm, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Alarm{cfg: cfg, priority: PriorityFromSeverity(cfg.Severity)}, nil
}

// Config returns the alarm's configuration.
func (a *Alarm) Config() Config { return a.cfg }

// State returns the displayed state: out of service, suppressed and shelved
// win, in that order, over the alarm state.
func (a *Alarm) State() State {
	switch {
	case a.disabled:
		return OutOfService
	case a.suppressed:
		return Suppressed
	case a.shelved:
		return Shelved
	}
	return a.base
}

func (a *Alarm) hidden() bool { return a.disabled || a.suppressed || a.shelved }

// SetCondition reports the (already filtered) alarm condition.
func (a *Alarm) SetCondition(active bool, at time.Time) []Event {
	if active == a.cond {
		return nil
	}
	a.cond = active
	var events []Event
	if !active && a.shelved && a.oneShot {
		events = append(events, a.unshelve(at, "", "condition returned to normal"))
	}
	events = append(events, a.evaluate(at)...)
	if active {
		events = append(events, a.recordRise(at)...)
	}
	return events
}

// evaluate applies the condition to the alarm state. A hidden alarm is held
// at NORMAL, so when it is shown again an active condition re-annunciates it.
func (a *Alarm) evaluate(at time.Time) []Event {
	if a.hidden() {
		a.base = Normal
		return nil
	}
	prev := a.State()
	switch a.base {
	case Normal, RTNUnacked, LatchedUnacked, LatchedAcked:
		if !a.cond {
			return nil
		}
		a.base = ActiveUnacked
		if !a.cfg.AckRequired {
			a.base = ActiveAcked
			a.ackTime = at
		}
		a.count++
		a.inAlarmTime = at
		return []Event{a.event(Activated, prev, at, "", "")}
	case ActiveUnacked, ActiveAcked:
		if a.cond {
			return nil
		}
		switch {
		case a.cfg.Latched && a.base == ActiveUnacked:
			a.base = LatchedUnacked
		case a.cfg.Latched:
			a.base = LatchedAcked
		case a.base == ActiveUnacked:
			a.base = RTNUnacked
		default:
			a.base = Normal
		}
		a.rtnTime = at
		return []Event{a.event(ReturnedToNormal, prev, at, "", "")}
	}
	return nil
}

// Ack acknowledges the alarm.
func (a *Alarm) Ack(user string, at time.Time) ([]Event, error) {
	if a.disabled {
		return nil, ErrOutOfService
	}
	prev := a.State()
	switch a.base {
	case ActiveUnacked:
		a.base = ActiveAcked
	case RTNUnacked:
		a.base = Normal
	case LatchedUnacked:
		a.base = LatchedAcked
	default:
		return nil, ErrNotUnacked
	}
	a.ackTime = at
	return []Event{a.event(Acknowledged, prev, at, user, "")}, nil
}

// Reset resets a latched alarm whose condition has cleared and that has been acknowledged.
func (a *Alarm) Reset(user string, at time.Time) ([]Event, error) {
	if a.base != LatchedAcked {
		return nil, ErrNotResettable
	}
	prev := a.State()
	a.base = Normal
	a.resetTime = at
	return []Event{a.event(Reset, prev, at, user, "")}, nil
}

// Shelve hides the alarm for d, capped at MaxShelve; d <= 0 means MaxShelve.
// With oneShot the shelve also ends when the condition returns to normal.
// Shelving a shelved alarm restarts the shelve.
func (a *Alarm) Shelve(user string, d time.Duration, oneShot bool, at time.Time) ([]Event, error) {
	switch {
	case a.disabled:
		return nil, ErrOutOfService
	case a.cfg.MaxShelve <= 0:
		return nil, ErrShelveNotPermitted
	case oneShot && !a.cond:
		return nil, ErrNotActive
	}
	if d <= 0 || d > a.cfg.MaxShelve {
		d = a.cfg.MaxShelve
	}
	prev := a.State()
	a.shelved = true
	a.oneShot = oneShot
	a.shelveTime = at
	a.shelveExpiry = at.Add(d)
	a.base = Normal
	detail := "for " + d.String()
	if oneShot {
		detail = "one-shot, at most " + d.String()
	}
	return []Event{a.event(ShelvedEvent, prev, at, user, detail)}, nil
}

// Unshelve ends a shelve. It does nothing if the alarm is not shelved.
func (a *Alarm) Unshelve(user string, at time.Time) []Event {
	if !a.shelved {
		return nil
	}
	return append([]Event{a.unshelve(at, user, "")}, a.evaluate(at)...)
}

func (a *Alarm) unshelve(at time.Time, user, detail string) Event {
	prev := a.State()
	a.shelved = false
	a.oneShot = false
	a.unshelveTime = at
	return a.event(UnshelvedEvent, prev, at, user, detail)
}

// Suppress suppresses the alarm by design.
func (a *Alarm) Suppress(user string, at time.Time) []Event {
	if a.suppressed {
		return nil
	}
	prev := a.State()
	a.suppressed = true
	a.base = Normal
	return []Event{a.event(SuppressedEvent, prev, at, user, "")}
}

// Unsuppress ends suppression; an active condition re-annunciates the alarm.
func (a *Alarm) Unsuppress(user string, at time.Time) []Event {
	if !a.suppressed {
		return nil
	}
	prev := a.State()
	a.suppressed = false
	return append([]Event{a.event(UnsuppressedEvent, prev, at, user, "")}, a.evaluate(at)...)
}

// Disable takes the alarm out of service. It also ends any shelve.
func (a *Alarm) Disable(user string, at time.Time) []Event {
	if a.disabled {
		return nil
	}
	var events []Event
	if a.shelved {
		events = append(events, a.unshelve(at, user, "out of service"))
	}
	prev := a.State()
	a.disabled = true
	a.base = Normal
	return append(events, a.event(Disabled, prev, at, user, ""))
}

// Enable returns the alarm to service; an active condition re-annunciates it.
func (a *Alarm) Enable(user string, at time.Time) []Event {
	if !a.disabled {
		return nil
	}
	prev := a.State()
	a.disabled = false
	return append([]Event{a.event(Enabled, prev, at, user, "")}, a.evaluate(at)...)
}

// ResetCount sets the alarm count to zero.
func (a *Alarm) ResetCount(user string, at time.Time) []Event {
	a.count = 0
	a.countResetTime = at
	return []Event{a.event(CountReset, a.State(), at, user, "")}
}

// NextDeadline returns when Tick next has work to do: a shelve expires or
// chattering ends. ok is false when no deadline is pending.
func (a *Alarm) NextDeadline() (deadline time.Time, ok bool) {
	if a.shelved {
		deadline, ok = a.shelveExpiry, true
	}
	if a.chattering && len(a.rises) > 0 {
		if end := a.rises[0].Add(a.cfg.ChatterWindow); !ok || end.Before(deadline) {
			deadline, ok = end, true
		}
	}
	return deadline, ok
}

// Tick ends an expired shelve and clears chattering once activations age out
// of the chatter window.
func (a *Alarm) Tick(at time.Time) []Event {
	var events []Event
	if a.shelved && !at.Before(a.shelveExpiry) {
		events = append(events, a.unshelve(at, "", "expired"))
		events = append(events, a.evaluate(at)...)
	}
	return append(events, a.updateChatter(at)...)
}

// recordRise remembers a condition activation for chattering detection. It
// counts every activation, also while the alarm is hidden.
func (a *Alarm) recordRise(at time.Time) []Event {
	if a.cfg.ChatterCount == 0 {
		return nil
	}
	a.rises = append(a.rises, at)
	if len(a.rises) > a.cfg.ChatterCount {
		a.rises = a.rises[len(a.rises)-a.cfg.ChatterCount:]
	}
	return a.updateChatter(at)
}

func (a *Alarm) updateChatter(at time.Time) []Event {
	if a.cfg.ChatterCount == 0 {
		return nil
	}
	chattering := len(a.rises) == a.cfg.ChatterCount && at.Sub(a.rises[0]) < a.cfg.ChatterWindow
	if chattering == a.chattering {
		return nil
	}
	a.chattering = chattering
	kind := ChatterEnded
	if chattering {
		kind = ChatterStarted
	}
	return []Event{a.event(kind, a.State(), at, "", "")}
}

func (a *Alarm) event(kind EventKind, prev State, at time.Time, user, detail string) Event {
	return Event{
		Time:        at,
		Alarm:       a.cfg.ID,
		Description: a.cfg.Description,
		Kind:        kind,
		State:       a.State(),
		Previous:    prev,
		Priority:    a.priority,
		User:        user,
		Detail:      detail,
	}
}

// Status returns a snapshot of the alarm.
func (a *Alarm) Status() Status {
	return Status{
		ID:             a.cfg.ID,
		Description:    a.cfg.Description,
		State:          a.State(),
		Condition:      a.cond,
		Shelved:        a.shelved,
		Suppressed:     a.suppressed,
		Disabled:       a.disabled,
		Chattering:     a.chattering,
		OneShot:        a.oneShot,
		Severity:       a.cfg.Severity,
		Priority:       a.priority,
		Count:          a.count,
		InAlarmTime:    a.inAlarmTime,
		AckTime:        a.ackTime,
		RTNTime:        a.rtnTime,
		ResetTime:      a.resetTime,
		ShelveTime:     a.shelveTime,
		UnshelveTime:   a.unshelveTime,
		ShelveExpiry:   a.shelveExpiry,
		CountResetTime: a.countResetTime,
	}
}
