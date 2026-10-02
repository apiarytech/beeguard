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

// EventKind is what happened to an alarm.
type EventKind uint8

const (
	Activated        EventKind = iota + 1 // the alarm was annunciated
	ReturnedToNormal                      // the condition cleared
	Acknowledged
	Reset // a latched alarm was reset
	ShelvedEvent
	UnshelvedEvent
	SuppressedEvent
	UnsuppressedEvent
	Disabled // taken out of service
	Enabled  // returned to service
	ChatterStarted
	ChatterEnded
	CountReset
)

var eventNames = [...]string{
	"", "ACTIVATED", "RTN", "ACKNOWLEDGED", "RESET", "SHELVED", "UNSHELVED",
	"SUPPRESSED", "UNSUPPRESSED", "DISABLED", "ENABLED", "CHATTER_STARTED",
	"CHATTER_ENDED", "COUNT_RESET",
}

func (k EventKind) String() string {
	if k > 0 && int(k) < len(eventNames) {
		return eventNames[k]
	}
	return fmt.Sprintf("EventKind(%d)", uint8(k))
}

// ParseEventKind is the inverse of EventKind.String.
func ParseEventKind(s string) (EventKind, error) {
	for k := 1; k < len(eventNames); k++ {
		if eventNames[k] == s {
			return EventKind(k), nil
		}
	}
	return 0, fmt.Errorf("alarm: unknown event kind %q", s)
}

// Event is one entry in the alarm and event journal.
type Event struct {
	Time        time.Time // when the alarm system recorded the event
	SourceTime  time.Time // device time of the value that caused it; zero for operator actions
	Alarm       string
	Description string
	Kind        EventKind
	State       State // displayed state after the event
	Previous    State // displayed state before the event
	Priority    Priority
	User        string // who issued the command; empty for condition changes
	Detail      string
}
