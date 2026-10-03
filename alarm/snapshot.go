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

// Snapshot is an alarm's state, to keep it across a restart: the ISA-18.2
// state and acknowledgement, shelving, suppression, out-of-service, chatter,
// the activation count and their times. It holds no configuration: it is
// restored into an alarm built from the current definition.
type Snapshot struct {
	Condition  bool        `json:"condition"`
	Base       State       `json:"base"`
	Shelved    bool        `json:"shelved,omitempty"`
	OneShot    bool        `json:"oneShot,omitempty"`
	Suppressed bool        `json:"suppressed,omitempty"`
	Disabled   bool        `json:"disabled,omitempty"`
	Chattering bool        `json:"chattering,omitempty"`
	Rises      []time.Time `json:"rises,omitempty"`
	Count      int         `json:"count,omitempty"`

	InAlarmTime    time.Time `json:"inAlarmTime,omitzero"`
	AckTime        time.Time `json:"ackTime,omitzero"`
	RTNTime        time.Time `json:"rtnTime,omitzero"`
	ResetTime      time.Time `json:"resetTime,omitzero"`
	ShelveTime     time.Time `json:"shelveTime,omitzero"`
	UnshelveTime   time.Time `json:"unshelveTime,omitzero"`
	ShelveExpiry   time.Time `json:"shelveExpiry,omitzero"`
	CountResetTime time.Time `json:"countResetTime,omitzero"`
}

// Snapshot returns the alarm's state.
func (a *Alarm) Snapshot() Snapshot {
	return Snapshot{
		Condition: a.cond, Base: a.base, Shelved: a.shelved, OneShot: a.oneShot,
		Suppressed: a.suppressed, Disabled: a.disabled, Chattering: a.chattering,
		Rises: append([]time.Time(nil), a.rises...), Count: a.count,
		InAlarmTime: a.inAlarmTime, AckTime: a.ackTime, RTNTime: a.rtnTime, ResetTime: a.resetTime,
		ShelveTime: a.shelveTime, UnshelveTime: a.unshelveTime, ShelveExpiry: a.shelveExpiry,
		CountResetTime: a.countResetTime,
	}
}

// Restore gives the alarm a state saved by Snapshot. It emits no events: the
// state is what the journal last recorded. A latched state for an alarm that
// is no longer latched, or a shelve the definition no longer allows, is
// refused.
func (a *Alarm) Restore(s Snapshot) error {
	switch s.Base {
	case Normal, ActiveUnacked, ActiveAcked, RTNUnacked:
	case LatchedUnacked, LatchedAcked:
		if !a.cfg.Latched {
			return fmt.Errorf("alarm %s: state %s, but it is not latched", a.cfg.ID, s.Base)
		}
	default:
		return fmt.Errorf("alarm %s: %s is not an alarm state", a.cfg.ID, s.Base)
	}
	a.cond, a.base = s.Condition, s.Base
	a.shelved, a.oneShot, a.suppressed, a.disabled, a.chattering = s.Shelved, s.OneShot, s.Suppressed, s.Disabled, s.Chattering
	a.rises, a.count = append([]time.Time(nil), s.Rises...), s.Count
	a.inAlarmTime, a.ackTime, a.rtnTime, a.resetTime = s.InAlarmTime, s.AckTime, s.RTNTime, s.ResetTime
	a.shelveTime, a.unshelveTime, a.shelveExpiry, a.countResetTime = s.ShelveTime, s.UnshelveTime, s.ShelveExpiry, s.CountResetTime
	return nil
}
