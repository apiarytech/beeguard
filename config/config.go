/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package config loads alarm definitions from JSON. The form follows the
// honeycomb plc4x connector configuration:
//
//	{
//	  "alarms": [{
//	    "id": "Guard1.TempHigh",
//	    "description": "Guard 1 brood temperature high",
//	    "source": "Guard1.Temp",
//	    "kind": "high",
//	    "limit": 38, "deadband": 0.5,
//	    "onDelay": "5s", "offDelay": "5s",
//	    "severity": 750, "ackRequired": true, "latched": false,
//	    "maxShelve": "8h", "chatterCount": 3, "chatterWindow": "60s"
//	  }]
//	}
//
// "kind" is digital, high, low, rate-of-rise, rate-of-fall or bad-quality.
// "invert" applies to digital and "period" to the rates. Durations are Go
// durations ("500ms", "15s", "8h"). "severity" defaults to 500 and
// "ackRequired" to true, the safer choice for an alarm. Unknown keys
// are rejected, so a misspelled key fails at startup instead of being ignored.
//
// "journal" sets how the journal is kept: {"retention": "8760h"} deletes
// events older than a year (ParseConfig).
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
)

type fileAlarm struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Kind        evaluator.Kind `json:"kind"`
	Limit       float64        `json:"limit"`
	Deadband    float64        `json:"deadband"`
	Invert      bool           `json:"invert"`
	Period      string         `json:"period"`
	OnDelay     string         `json:"onDelay"`
	OffDelay    string         `json:"offDelay"`

	Severity      int    `json:"severity"`
	AckRequired   *bool  `json:"ackRequired"`
	Latched       bool   `json:"latched"`
	MaxShelve     string `json:"maxShelve"`
	ChatterCount  int    `json:"chatterCount"`
	ChatterWindow string `json:"chatterWindow"`
}

// fileJournal is the journal's settings in an alarms file.
type fileJournal struct {
	// Retention: events older than this are deleted ("8760h"); empty keeps
	// them for ever.
	Retention string `json:"retention,omitempty"`
}

type file struct {
	Alarms  []fileAlarm  `json:"alarms"`
	Journal *fileJournal `json:"journal,omitempty"`
}

// FileType is the Go type of an alarms file, for tools that describe the
// form, such as a JSON Schema generator.
func FileType() reflect.Type { return reflect.TypeOf(file{}) }

// Config is an alarms file: the definitions and the journal's settings.
type Config struct {
	Alarms  []engine.Definition
	Journal Journal
}

// Journal is how the journal is kept.
type Journal struct {
	// Retention: events older than this are deleted, by whoever runs the
	// journal (journal.Purger); zero keeps them for ever.
	Retention time.Duration
}

// ParseConfig reads an alarms file: the definitions and, under "journal",
// the journal's settings:
//
//	{"alarms": [...], "journal": {"retention": "8760h"}}
func ParseConfig(r io.Reader) (Config, error) {
	var f file
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Config{}, err
	}
	var c Config
	if f.Journal != nil && f.Journal.Retention != "" {
		d, err := time.ParseDuration(f.Journal.Retention)
		if err != nil || d < 0 {
			return Config{}, fmt.Errorf("journal: retention %q: a positive duration such as \"8760h\"", f.Journal.Retention)
		}
		c.Journal.Retention = d
	}
	c.Alarms = make([]engine.Definition, 0, len(f.Alarms))
	for _, fa := range f.Alarms {
		def, err := fa.definition()
		if err != nil {
			return Config{}, fmt.Errorf("alarm %q: %w", fa.ID, err)
		}
		c.Alarms = append(c.Alarms, def)
	}
	return c, nil
}

// Load reads alarm definitions from a JSON file.
func Load(path string) ([]engine.Definition, error) {
	c, err := LoadConfig(path)
	return c.Alarms, err
}

// LoadConfig reads an alarms file: the definitions and the journal's
// settings.
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	defer f.Close()
	c, err := ParseConfig(f)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// Parse reads alarm definitions in the JSON form described in the package
// documentation. The journal's settings are checked but not returned:
// ParseConfig returns them.
func Parse(r io.Reader) ([]engine.Definition, error) {
	c, err := ParseConfig(r)
	if err != nil {
		return nil, err
	}
	return c.Alarms, nil
}

func (fa fileAlarm) definition() (engine.Definition, error) {
	var durations [5]time.Duration
	for i, s := range []string{fa.Period, fa.OnDelay, fa.OffDelay, fa.MaxShelve, fa.ChatterWindow} {
		if s == "" {
			continue
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return engine.Definition{}, err
		}
		durations[i] = d
	}
	severity := fa.Severity
	if severity == 0 {
		severity = 500
	}
	ackRequired := fa.AckRequired == nil || *fa.AckRequired
	return engine.Definition{
		Alarm: alarm.Config{
			ID:            fa.ID,
			Description:   fa.Description,
			Severity:      severity,
			AckRequired:   ackRequired,
			Latched:       fa.Latched,
			MaxShelve:     durations[3],
			ChatterCount:  fa.ChatterCount,
			ChatterWindow: durations[4],
		},
		Source: fa.Source,
		Condition: evaluator.Config{
			Kind:     fa.Kind,
			Limit:    fa.Limit,
			Deadband: fa.Deadband,
			Invert:   fa.Invert,
			Period:   durations[0],
			OnDelay:  durations[1],
			OffDelay: durations[2],
		},
	}, nil
}
