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
	"fmt"
	"time"
)

// State is an alarm's ISA-18.2 state. Its numeric values match the STATE
// output of the BG_ALM_DIG function block (reference/digital_alarm.st).
type State uint8

const (
	Normal         State = iota // A: condition clear, nothing to acknowledge
	ActiveUnacked               // B: condition active, unacknowledged
	ActiveAcked                 // C: condition active, acknowledged
	RTNUnacked                  // D: condition cleared before it was acknowledged
	LatchedUnacked              // E: latched, condition cleared, unacknowledged
	LatchedAcked                // E: latched, condition cleared, acknowledged, waiting for reset
	Shelved                     // F: hidden by the operator for a limited time
	Suppressed                  // G: suppressed by design
	OutOfService                // H: disabled, e.g. for maintenance
)

var stateNames = [...]string{
	"NORMAL", "ACTIVE_UNACK", "ACTIVE_ACK", "RTN_UNACK", "LATCHED_UNACK",
	"LATCHED_ACK", "SHELVED", "SUPPRESSED", "OUT_OF_SERVICE",
}

func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

// ParseState is the inverse of State.String.
func ParseState(s string) (State, error) {
	for i, name := range stateNames {
		if name == s {
			return State(i), nil
		}
	}
	return 0, fmt.Errorf("alarm: unknown state %q", s)
}

// ISA returns the state's letter in the ISA-18.2 state diagram.
func (s State) ISA() string {
	if s > OutOfService {
		return "?"
	}
	return string("ABCDEEFGH"[s])
}

// Annunciated reports whether the state is shown to the operator as an alarm.
func (s State) Annunciated() bool {
	return s == ActiveUnacked || s == ActiveAcked || s == LatchedUnacked || s == LatchedAcked
}

// Unacked reports whether the state is waiting for an acknowledgement.
func (s State) Unacked() bool {
	return s == ActiveUnacked || s == RTNUnacked || s == LatchedUnacked
}

// Priority is an alarm's priority, 1 (urgent) to 4 (low).
type Priority uint8

const (
	Urgent Priority = 1
	High   Priority = 2
	Medium Priority = 3
	Low    Priority = 4
)

func (p Priority) String() string {
	switch p {
	case Urgent:
		return "URGENT"
	case High:
		return "HIGH"
	case Medium:
		return "MEDIUM"
	case Low:
		return "LOW"
	}
	return fmt.Sprintf("Priority(%d)", uint8(p))
}

// PriorityFromSeverity maps a severity of 1..1000 (1000 most severe) to a
// priority in four equal bands of 250.
func PriorityFromSeverity(severity int) Priority {
	switch {
	case severity <= 250:
		return Low
	case severity <= 500:
		return Medium
	case severity <= 750:
		return High
	default:
		return Urgent
	}
}

// Status is a snapshot of an alarm, the equivalent of the function block's outputs.
type Status struct {
	ID          string
	Description string
	State       State // displayed state: OutOfService, Suppressed or Shelved win over the alarm state
	Condition   bool  // the condition as last reported, also while the alarm is hidden
	Shelved     bool
	Suppressed  bool
	Disabled    bool
	Chattering  bool
	OneShot     bool // the current shelve ends when the condition returns to normal
	Severity    int
	Priority    Priority
	Count       int // annunciated activations since the last count reset

	InAlarmTime    time.Time
	AckTime        time.Time
	RTNTime        time.Time
	ResetTime      time.Time
	ShelveTime     time.Time
	UnshelveTime   time.Time
	ShelveExpiry   time.Time
	CountResetTime time.Time
}

// InAlarm reports whether the alarm is active or latched (INALARM).
func (s Status) InAlarm() bool { return s.State.Annunciated() }

// Unacked reports whether an acknowledgement is pending (UNACKED).
func (s Status) Unacked() bool { return s.State.Unacked() }

// Acked is the inverse of Unacked (ACKED).
func (s Status) Acked() bool { return !s.State.Unacked() }

// InAlarmUnack reports whether the alarm is in alarm and unacknowledged (INALARMUNACK).
func (s Status) InAlarmUnack() bool { return s.InAlarm() && s.Unacked() }
