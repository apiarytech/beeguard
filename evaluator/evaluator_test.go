/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package evaluator

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func newEval(t *testing.T, cfg Config) *Evaluator {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func good(v float64) Sample { return Sample{Value: v, Good: true} }
func bad(v float64) Sample  { return Sample{Value: v} }

// step feeds values one millisecond apart and returns Active after each.
func step(e *Evaluator, start int, values ...float64) []bool {
	out := make([]bool, len(values))
	for i, v := range values {
		e.Update(good(v), at(start+i))
		out[i] = e.Active()
	}
	return out
}

func equal(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDigital(t *testing.T) {
	e := newEval(t, Config{Kind: Digital})
	if got := step(e, 0, 0, 1, 1, 0); !equal(got, []bool{false, true, true, false}) {
		t.Fatalf("digital = %v", got)
	}
	inv := newEval(t, Config{Kind: Digital, Invert: true})
	if got := step(inv, 0, 1, 0, 1); !equal(got, []bool{false, true, false}) {
		t.Fatalf("inverted = %v", got)
	}
}

func TestHighDeadband(t *testing.T) {
	e := newEval(t, Config{Kind: High, Limit: 38, Deadband: 1})
	got := step(e, 0, 37, 38, 38.1, 37.5, 37.0, 36.9)
	want := []bool{false, false, true, true, true, false}
	if !equal(got, want) {
		t.Fatalf("high = %v, want %v", got, want)
	}
}

func TestLowDeadband(t *testing.T) {
	e := newEval(t, Config{Kind: Low, Limit: 10, Deadband: 2})
	got := step(e, 0, 11, 9.9, 11.5, 12, 12.1)
	want := []bool{false, true, true, true, false}
	if !equal(got, want) {
		t.Fatalf("low = %v, want %v", got, want)
	}
}

func TestOnDelayExpiresOnTick(t *testing.T) {
	e := newEval(t, Config{Kind: High, Limit: 10, OnDelay: time.Second})
	if e.Update(good(11), at(0)) {
		t.Fatal("active before the on-delay")
	}
	if e.Tick(at(999)) {
		t.Fatal("active 1 ms early")
	}
	if !e.Tick(at(1000)) || !e.Active() {
		t.Fatal("not active after the on-delay")
	}
}

func TestOnDelayRestartsWhenConditionDrops(t *testing.T) {
	e := newEval(t, Config{Kind: High, Limit: 10, OnDelay: time.Second})
	e.Update(good(11), at(0))
	e.Update(good(9), at(500))
	e.Update(good(11), at(600))
	if e.Tick(at(1500)) {
		t.Fatal("the on-delay must restart when the condition drops")
	}
	if !e.Tick(at(1600)) {
		t.Fatal("not active one delay after the restart")
	}
}

func TestOffDelay(t *testing.T) {
	e := newEval(t, Config{Kind: Digital, OffDelay: 2 * time.Second})
	e.Update(good(1), at(0))
	e.Update(good(0), at(100))
	if !e.Active() || e.Tick(at(2099)) {
		t.Fatal("cleared before the off-delay")
	}
	if !e.Tick(at(2100)) || e.Active() {
		t.Fatal("still active after the off-delay")
	}
}

func TestBadQualityHoldsCondition(t *testing.T) {
	e := newEval(t, Config{Kind: High, Limit: 10})
	e.Update(good(11), at(0))
	e.Update(bad(0), at(1))
	if !e.Active() {
		t.Fatal("bad data cleared the alarm")
	}
	e.Update(good(5), at(2))
	e.Update(bad(99), at(3))
	if e.Active() {
		t.Fatal("bad data raised the alarm")
	}
}

func TestBadQualityKind(t *testing.T) {
	e := newEval(t, Config{Kind: BadQuality, OnDelay: time.Second})
	e.Update(bad(0), at(0))
	if !e.Tick(at(1000)) || !e.Active() {
		t.Fatal("bad quality not alarmed")
	}
	e.Update(good(0), at(1001))
	if e.Active() {
		t.Fatal("good quality did not clear the alarm")
	}
}

func TestRateOfRise(t *testing.T) {
	e := newEval(t, Config{Kind: RateOfRise, Limit: 1, Period: time.Second}) // 1 EU/s
	e.Update(good(10), at(0))                                                // primes
	e.Update(good(11), at(500))                                              // inside the period
	if e.Active() {
		t.Fatal("rate measured inside the period")
	}
	e.Update(good(12.5), at(1000)) // 2.5 EU/s
	if !e.Active() || e.Rate() != 2.5 {
		t.Fatalf("active = %v, rate = %v", e.Active(), e.Rate())
	}
	// The value stops changing: Tick samples the rate without new updates.
	if !e.Tick(at(2000)) || e.Active() || e.Rate() != 0 {
		t.Fatalf("after a flat period active = %v, rate = %v", e.Active(), e.Rate())
	}
}

func TestRateOfFall(t *testing.T) {
	e := newEval(t, Config{Kind: RateOfFall, Limit: 1, Period: time.Second})
	e.Update(good(10), at(0))
	e.Update(good(9.5), at(1000)) // -0.5 EU/s
	if e.Active() {
		t.Fatal("slow fall alarmed")
	}
	e.Update(good(7), at(2000)) // -2.5 EU/s
	if !e.Active() {
		t.Fatal("fast fall not alarmed")
	}
}

func TestRateRestartsAfterBadQuality(t *testing.T) {
	e := newEval(t, Config{Kind: RateOfRise, Limit: 1, Period: time.Second})
	e.Update(good(10), at(0))
	e.Update(bad(0), at(500))
	e.Update(good(100), at(1000)) // re-primes; the jump is not a rate
	if e.Active() {
		t.Fatal("the jump across bad data was measured as a rate")
	}
	e.Update(good(100.5), at(2000))
	if e.Active() || e.Rate() != 0.5 {
		t.Fatalf("active = %v, rate = %v", e.Active(), e.Rate())
	}
}

func TestTickBeforeAnySample(t *testing.T) {
	e := newEval(t, Config{Kind: RateOfRise, Limit: 1, Period: time.Second})
	if e.Tick(at(5000)) || e.Active() {
		t.Fatal("tick without samples changed the condition")
	}
}

func TestValidate(t *testing.T) {
	bad := []Config{
		{},
		{Kind: 99},
		{Kind: High, Deadband: -1},
		{Kind: High, OnDelay: -1},
		{Kind: RateOfRise, Limit: 1},
		{Kind: RateOfFall, Limit: -1, Period: time.Second},
	}
	for _, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) accepted an invalid config", cfg)
		}
	}
}

func TestKindText(t *testing.T) {
	var k Kind
	if err := k.UnmarshalText([]byte("rate-of-fall")); err != nil || k != RateOfFall {
		t.Fatalf("UnmarshalText = %v, %v", k, err)
	}
	if err := k.UnmarshalText([]byte("sideways")); err == nil {
		t.Fatal("accepted an unknown kind")
	}
	if text, _ := High.MarshalText(); string(text) != "high" {
		t.Fatalf("MarshalText = %s", text)
	}
}

func TestNextDeadline(t *testing.T) {
	e := newEval(t, Config{Kind: High, Limit: 10, OnDelay: time.Second, OffDelay: 2 * time.Second})
	if _, ok := e.NextDeadline(); ok {
		t.Fatal("deadline with nothing pending")
	}
	e.Update(good(11), at(100))
	if d, ok := e.NextDeadline(); !ok || !d.Equal(at(1100)) {
		t.Fatalf("on-delay deadline = %v, %v", d, ok)
	}
	e.Tick(at(1100))
	e.Update(good(5), at(1200))
	if d, ok := e.NextDeadline(); !ok || !d.Equal(at(3200)) {
		t.Fatalf("off-delay deadline = %v, %v", d, ok)
	}

	r := newEval(t, Config{Kind: RateOfRise, Limit: 1, Period: time.Second})
	r.Update(good(1), at(0))
	if d, ok := r.NextDeadline(); !ok || !d.Equal(at(1000)) {
		t.Fatalf("rate deadline = %v, %v", d, ok)
	}
}
