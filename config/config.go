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

// FileType is the Go type of an alarms file, for tools that describe the
// form, such as a JSON Schema generator.
func FileType() reflect.Type {
	return reflect.TypeOf(struct {
		Alarms []fileAlarm `json:"alarms"`
	}{})
}

// Load reads alarm definitions from a JSON file.
func Load(path string) ([]engine.Definition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	defer f.Close()
	defs, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return defs, nil
}

// Parse reads alarm definitions in the JSON form described in the package documentation.
func Parse(r io.Reader) ([]engine.Definition, error) {
	var file struct {
		Alarms []fileAlarm `json:"alarms"`
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, err
	}
	defs := make([]engine.Definition, 0, len(file.Alarms))
	for _, fa := range file.Alarms {
		def, err := fa.definition()
		if err != nil {
			return nil, fmt.Errorf("alarm %q: %w", fa.ID, err)
		}
		defs = append(defs, def)
	}
	return defs, nil
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
