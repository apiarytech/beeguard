/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
)

func TestParse(t *testing.T) {
	defs, err := Parse(strings.NewReader(`{"alarms": [
		{"id": "Guard1.TempHigh", "description": "Temp high", "source": "Guard1.Temp", "kind": "high",
		 "limit": 38, "deadband": 0.5, "onDelay": "5s", "offDelay": "2s", "severity": 750,
		 "latched": true, "maxShelve": "8h", "chatterCount": 3, "chatterWindow": "1m"},
		{"id": "Guard1.WeightDrop", "source": "Guard1.Weight", "kind": "rate-of-fall", "limit": 0.05,
		 "period": "10s", "ackRequired": false},
		{"id": "Guard1.LidOpen", "source": "Guard1.Lid", "kind": "digital", "invert": true}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 3 {
		t.Fatalf("defs = %d", len(defs))
	}
	h := defs[0]
	if h.Alarm.ID != "Guard1.TempHigh" || h.Alarm.Severity != 750 || !h.Alarm.AckRequired || !h.Alarm.Latched ||
		h.Alarm.MaxShelve != 8*time.Hour || h.Alarm.ChatterCount != 3 || h.Alarm.ChatterWindow != time.Minute ||
		h.Source != "Guard1.Temp" || h.Condition.Kind != evaluator.High || h.Condition.Limit != 38 ||
		h.Condition.Deadband != 0.5 || h.Condition.OnDelay != 5*time.Second || h.Condition.OffDelay != 2*time.Second {
		t.Fatalf("high = %+v", h)
	}
	w := defs[1]
	if w.Alarm.Severity != 500 || w.Alarm.AckRequired || w.Condition.Period != 10*time.Second {
		t.Fatalf("defaults = %+v", w)
	}
	if !defs[2].Condition.Invert {
		t.Fatal("invert not read")
	}
	if _, err := engine.New(defs); err != nil {
		t.Fatalf("the definitions do not build an engine: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key":   `{"alarms": [{"id": "A", "sourse": "S"}]}`,
		"unknown kind":  `{"alarms": [{"id": "A", "kind": "sideways"}]}`,
		"bad duration":  `{"alarms": [{"id": "A", "kind": "high", "onDelay": "soon"}]}`,
		"not json":      `alarms:`,
		"bad retention": `{"alarms": [], "journal": {"retention": "a year"}}`,
		"journal key":   `{"alarms": [], "journal": {"keep": "8760h"}}`,
	} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alarms.json")
	if err := os.WriteFile(path, []byte(`{"alarms": [{"id": "A", "source": "S", "kind": "digital"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := Load(path)
	if err != nil || len(defs) != 1 {
		t.Fatalf("Load = %v, %v", defs, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("loaded a missing file")
	}
}

func TestParseConfigJournal(t *testing.T) {
	c, err := ParseConfig(strings.NewReader(`{"alarms": [{"id": "A", "source": "S", "kind": "digital"}], "journal": {"retention": "8760h"}}`))
	if err != nil || len(c.Alarms) != 1 || c.Journal.Retention != 8760*time.Hour {
		t.Fatalf("%+v, %v", c, err)
	}
	if c, err := ParseConfig(strings.NewReader(`{"alarms": []}`)); err != nil || c.Journal.Retention != 0 {
		t.Errorf("no journal: %+v, %v", c, err)
	}
}
