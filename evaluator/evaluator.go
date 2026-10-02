/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package evaluator turns process values into alarm conditions.
//
// It applies the ISA-TR18.2.3 filtering order: the limit with its deadband
// first, then the on-delay, then the off-delay. While a sample's quality is bad
// the condition holds its last value, so bad data can neither raise nor clear
// an alarm; use a BadQuality evaluator to alarm on the bad quality itself.
//
// An Evaluator is pure: it has no clock or goroutines. Update takes each new
// sample and Tick must be called periodically so delays expire and rates are
// sampled when the value does not change. An Evaluator is not safe for
// concurrent use.
package evaluator

import (
	"errors"
	"fmt"
	"time"
)

// Kind is the condition an Evaluator detects.
type Kind uint8

const (
	Digital    Kind = iota + 1 // value is non-zero (zero when Invert)
	High                       // value > Limit; clears when value < Limit - Deadband
	Low                        // value < Limit; clears when value > Limit + Deadband
	RateOfRise                 // rise over one Period > Limit per second
	RateOfFall                 // fall over one Period > Limit per second
	BadQuality                 // the sample's quality is bad
)

var kindNames = [...]string{"", "digital", "high", "low", "rate-of-rise", "rate-of-fall", "bad-quality"}

func (k Kind) String() string {
	if k > 0 && int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// ParseKind is the inverse of Kind.String.
func ParseKind(s string) (Kind, error) {
	for k := 1; k < len(kindNames); k++ {
		if kindNames[k] == s {
			return Kind(k), nil
		}
	}
	return 0, fmt.Errorf("evaluator: unknown kind %q", s)
}

// MarshalText implements encoding.TextMarshaler.
func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *Kind) UnmarshalText(text []byte) error {
	parsed, err := ParseKind(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

func (k Kind) isRate() bool { return k == RateOfRise || k == RateOfFall }

// Config describes one condition.
type Config struct {
	Kind     Kind
	Limit    float64       // High/Low: the alarm limit. Rates: EU per second, positive.
	Deadband float64       // High/Low: hysteresis on return to normal, in EU
	Invert   bool          // Digital: alarm when the value is zero
	Period   time.Duration // Rates: sampling period

	OnDelay  time.Duration // the condition must hold this long before it becomes active
	OffDelay time.Duration // the condition must be clear this long before it becomes inactive
}

// Validate reports a configuration the evaluator cannot run with.
func (c Config) Validate() error {
	switch {
	case c.Kind < Digital || c.Kind > BadQuality:
		return fmt.Errorf("evaluator: invalid kind %v", c.Kind)
	case c.Deadband < 0:
		return errors.New("evaluator: negative deadband")
	case c.OnDelay < 0 || c.OffDelay < 0:
		return errors.New("evaluator: negative delay")
	case c.Kind.isRate() && c.Period <= 0:
		return fmt.Errorf("evaluator: %v needs a positive period", c.Kind)
	case c.Kind.isRate() && c.Limit < 0:
		return fmt.Errorf("evaluator: %v limit must be positive", c.Kind)
	}
	return nil
}

// Sample is one process value.
type Sample struct {
	Value float64 // BOOL values are 0 or 1
	Good  bool    // false when the value's quality is bad
	Time  time.Time
}

// Evaluator detects one condition on a stream of samples.
type Evaluator struct {
	cfg    Config
	last   Sample
	have   bool
	raw    bool // condition before the on/off delays
	active bool // condition after the delays

	pending      bool // raw differs from active and a delay is running
	pendingSince time.Time

	rocPrimed bool
	rocValue  float64
	rocTime   time.Time
	rate      float64
}

// New returns an evaluator whose condition is inactive.
func New(cfg Config) (*Evaluator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Evaluator{cfg: cfg}, nil
}

// Active reports the filtered condition.
func (e *Evaluator) Active() bool { return e.active }

// Rate returns the last measured rate of change in EU per second.
func (e *Evaluator) Rate() float64 { return e.rate }

// Last returns the last sample passed to Update.
func (e *Evaluator) Last() Sample { return e.last }

// NextDeadline returns when Tick next has work to do: a running on- or
// off-delay expires, or a rate is due to be measured. ok is false when no
// deadline is pending.
func (e *Evaluator) NextDeadline() (deadline time.Time, ok bool) {
	if e.pending {
		delay := e.cfg.OffDelay
		if e.raw {
			delay = e.cfg.OnDelay
		}
		deadline, ok = e.pendingSince.Add(delay), true
	}
	if e.cfg.Kind.isRate() && e.rocPrimed {
		if due := e.rocTime.Add(e.cfg.Period); !ok || due.Before(deadline) {
			deadline, ok = due, true
		}
	}
	return deadline, ok
}

// Update evaluates a new sample at now and reports whether Active changed.
func (e *Evaluator) Update(s Sample, now time.Time) bool {
	e.last, e.have = s, true
	e.raw = e.detect(s, now)
	return e.filter(now)
}

// Tick expires delays and samples rates of change; it reports whether Active changed.
func (e *Evaluator) Tick(now time.Time) bool {
	if !e.have {
		return false
	}
	if e.cfg.Kind.isRate() {
		e.raw = e.detect(e.last, now)
	}
	return e.filter(now)
}

func (e *Evaluator) detect(s Sample, now time.Time) bool {
	if e.cfg.Kind == BadQuality {
		return !s.Good
	}
	if !s.Good {
		e.rocPrimed = false // never measure a rate across bad data
		return e.raw
	}
	v := s.Value
	switch e.cfg.Kind {
	case Digital:
		return (v != 0) != e.cfg.Invert
	case High:
		if v > e.cfg.Limit {
			return true
		}
		if v < e.cfg.Limit-e.cfg.Deadband {
			return false
		}
	case Low:
		if v < e.cfg.Limit {
			return true
		}
		if v > e.cfg.Limit+e.cfg.Deadband {
			return false
		}
	case RateOfRise, RateOfFall:
		return e.detectRate(v, now)
	}
	return e.raw // inside the deadband
}

func (e *Evaluator) detectRate(v float64, now time.Time) bool {
	if !e.rocPrimed {
		e.rocPrimed, e.rocValue, e.rocTime = true, v, now
		return e.raw
	}
	elapsed := now.Sub(e.rocTime)
	if elapsed < e.cfg.Period {
		return e.raw
	}
	e.rate = (v - e.rocValue) / elapsed.Seconds()
	e.rocValue, e.rocTime = v, now
	if e.cfg.Kind == RateOfRise {
		return e.rate > e.cfg.Limit
	}
	return e.rate < -e.cfg.Limit
}

func (e *Evaluator) filter(now time.Time) bool {
	if e.raw == e.active {
		e.pending = false
		return false
	}
	delay := e.cfg.OffDelay
	if e.raw {
		delay = e.cfg.OnDelay
	}
	if !e.pending {
		e.pending, e.pendingSince = true, now
	}
	if now.Sub(e.pendingSince) < delay {
		return false
	}
	e.active, e.pending = e.raw, false
	return true
}
