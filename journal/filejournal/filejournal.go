/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package filejournal stores the alarm journal in a JSON Lines file: one event
// per line, appended. It needs nothing outside the Go standard library.
//
// Each Publish writes its events with one append and, by default, syncs the
// file to disk before returning, so an acknowledged event survives a power
// loss. A line left half-written by a crash is ignored when reading.
//
// Query reads the whole file, so it suits journals of up to a few hundred
// thousand events. Rotate or archive the file for longer histories, or use
// sqljournal.
package filejournal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/journal"
)

// record is the JSON form of one event.
type record struct {
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

func toRecord(e alarm.Event) record {
	r := record{
		Time: e.Time, Alarm: e.Alarm, Description: e.Description, Kind: e.Kind.String(),
		State: e.State.String(), Previous: e.Previous.String(), Priority: int(e.Priority),
		User: e.User, Detail: e.Detail,
	}
	if !e.SourceTime.IsZero() {
		st := e.SourceTime
		r.SourceTime = &st
	}
	return r
}

func (r record) event() (alarm.Event, error) {
	e := alarm.Event{
		Time: r.Time, Alarm: r.Alarm, Description: r.Description, Priority: alarm.Priority(r.Priority),
		User: r.User, Detail: r.Detail,
	}
	if r.SourceTime != nil {
		e.SourceTime = *r.SourceTime
	}
	var err error
	if e.Kind, err = alarm.ParseEventKind(r.Kind); err != nil {
		return e, err
	}
	if e.State, err = alarm.ParseState(r.State); err != nil {
		return e, err
	}
	e.Previous, err = alarm.ParseState(r.Previous)
	return e, err
}

// Option configures a Journal.
type Option func(*Journal)

// WithoutSync skips syncing the file after each Publish: faster, but events
// written just before a power loss may be lost.
func WithoutSync() Option { return func(j *Journal) { j.sync = false } }

// Journal is a journal.Journal backed by a JSON Lines file. It is safe for concurrent use.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	sync bool
}

var _ journal.Journal = (*Journal)(nil)

// Open opens or creates the journal file for appending.
func Open(path string, opts ...Option) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("filejournal: %w", err)
	}
	j := &Journal{path: path, f: f, sync: true}
	for _, opt := range opts {
		opt(j)
	}
	return j, nil
}

// Close closes the file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Close()
}

// Publish implements engine.Sink.
func (j *Journal) Publish(_ context.Context, events []alarm.Event) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // Encode ends each record with '\n'
	for _, e := range events {
		if err := enc.Encode(toRecord(e)); err != nil {
			return fmt.Errorf("filejournal: %w", err)
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("filejournal: write: %w", err)
	}
	if j.sync {
		if err := j.f.Sync(); err != nil {
			return fmt.Errorf("filejournal: sync: %w", err)
		}
	}
	return nil
}

// Query returns the matching events, oldest first.
func (j *Journal) Query(_ context.Context, f journal.Filter) ([]alarm.Event, error) {
	j.mu.Lock() // a consistent view: no half-written batch
	defer j.mu.Unlock()
	file, err := os.Open(j.path)
	if err != nil {
		return nil, fmt.Errorf("filejournal: %w", err)
	}
	defer file.Close()

	var out []alarm.Event
	reader := bufio.NewReader(file)
	for line := 1; ; line++ {
		data, err := reader.ReadBytes('\n')
		complete := err == nil
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("filejournal: read: %w", err)
		}
		if len(bytes.TrimSpace(data)) > 0 {
			var r record
			decodeErr := json.Unmarshal(data, &r)
			var e alarm.Event
			if decodeErr == nil {
				e, decodeErr = r.event()
			}
			switch {
			case decodeErr == nil:
				if f.Match(e) {
					out = append(out, e)
				}
			case !complete:
				// The last line is incomplete: a write cut short by a crash.
			default:
				return nil, fmt.Errorf("filejournal: %s line %d: %w", j.path, line, decodeErr)
			}
		}
		if !complete {
			break
		}
	}
	return f.Window(out), nil
}

var _ journal.Purger = (*Journal)(nil)

// Purge drops the events recorded before before. It rewrites the file: the
// events kept go to a new file beside it, which then replaces it.
func (j *Journal) Purge(_ context.Context, before time.Time) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	data, err := os.ReadFile(j.path)
	if err != nil {
		return 0, fmt.Errorf("filejournal: %w", err)
	}
	var kept bytes.Buffer
	var n int64
	for line := range bytes.Lines(data) {
		var r record
		if !bytes.HasSuffix(line, []byte("\n")) {
			break // cut short by a crash
		}
		if err := json.Unmarshal(line, &r); err == nil && r.Time.Before(before) {
			n++
			continue
		}
		kept.Write(line)
	}
	if n == 0 {
		return 0, nil
	}
	tmp := j.path + ".purge"
	if err := os.WriteFile(tmp, kept.Bytes(), 0o640); err != nil {
		return 0, fmt.Errorf("filejournal: purge: %w", err)
	}
	if f, err := os.OpenFile(tmp, os.O_WRONLY, 0); err == nil {
		f.Sync()
		f.Close()
	}
	if err := j.f.Close(); err != nil {
		return 0, fmt.Errorf("filejournal: purge: %w", err)
	}
	renameErr := os.Rename(tmp, j.path)
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return 0, fmt.Errorf("filejournal: purge: reopen: %w", err)
	}
	j.f = f
	if renameErr != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("filejournal: purge: %w", renameErr)
	}
	return n, nil
}
