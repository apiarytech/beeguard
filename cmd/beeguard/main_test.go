/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apiarytech/honeycomb"
	plc "github.com/apiarytech/royaljelly/iec"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/api"
	"github.com/apiarytech/beeguard/internal/devcert"
	"github.com/apiarytech/beeguard/journal"
	"github.com/apiarytech/beeguard/journal/filejournal"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
}

func eventually(t *testing.T, what string, logs *syncBuffer, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; log:\n%s", what, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestEndToEnd links beeguard to a field honeycomb database over verified
// HTTPS, checks that alarms reach the journal, and drives beeguard's API.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, err := devcert.Ensure(filepath.Join(dir, "dev"))
	if err != nil {
		t.Fatal(err)
	}

	// The field side: a honeycomb database such as one fed by the PLC4X connector.
	field := honeycomb.NewTagDatabase()
	for _, tag := range []*honeycomb.Tag{
		{Name: "Guard1.Temp", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeREAL}, Value: plc.REAL(30)},
		{Name: "Guard1.Lid", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeBOOL}, Value: plc.BOOL(false)},
	} {
		if err := field.AddTag(tag); err != nil {
			t.Fatal(err)
		}
	}
	fieldPort := freePort(t)
	fieldCtx, stopField := context.WithCancel(context.Background())
	defer stopField()
	honeycomb.StartServer(field, []string{"field-token"}, fieldPort, certFile, keyFile, nil, fieldCtx)

	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("FIELD_TOKEN", "field-token")
	o := options{
		linksPath: write("links.json", fmt.Sprintf(`{"databases": [{"id": "field1", "url": "https://127.0.0.1:%s",
			"tokenEnv": "FIELD_TOKEN", "caFile": %q, "tags": ["Guard1.Temp", "Guard1.Lid"]}]}`, fieldPort, filepath.ToSlash(certFile))),
		alarmsPath: write("alarms.json", `{"alarms": [
			{"id": "Guard1.TempHigh", "source": "Guard1.Temp", "kind": "high", "limit": 38, "severity": 700},
			{"id": "Guard1.LidOpen", "source": "Guard1.Lid", "kind": "digital", "severity": 400}]}`),
		journalPath: filepath.Join(dir, "journal.jsonl"),
		tick:        time.Second,
		feedWait:    time.Second,
		skew:        5 * time.Second,
		poll:        50 * time.Millisecond,
		user:        "hmi",
		tagsPort:    freePort(t),
		apiPort:     freePort(t),
		cert:        certFile,
		key:         keyFile,
		token:       "beeguard-token",
		logFormat:   "text",
		logLevel:    "info",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &syncBuffer{}
	done := make(chan error)
	go func() { done <- run(ctx, o, logs) }()

	stamp := time.Now().Add(-time.Second).UTC().Truncate(time.Millisecond)
	if err := field.SetTagValueQualityAt("Guard1.Temp", plc.REAL(39), honeycomb.QualityGood, stamp); err != nil {
		t.Fatal(err)
	}
	if err := field.SetTagValue("Guard1.Lid", plc.BOOL(true)); err != nil {
		t.Fatal(err)
	}

	reader, err := filejournal.Open(o.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	activations := func() []alarm.Event {
		events, _ := reader.Query(ctx, journal.Filter{Kinds: []alarm.EventKind{alarm.Activated}})
		return events
	}
	eventually(t, "both alarms in the journal", logs, func() bool { return len(activations()) == 2 })
	for _, e := range activations() {
		if e.Alarm == "Guard1.TempHigh" && !e.SourceTime.Equal(stamp) {
			t.Fatalf("the device timestamp did not cross the link: %v, want %v", e.SourceTime, stamp)
		}
	}

	// beeguard's own API, over verified TLS.
	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(certFile)
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	call := func(method, path, body string) (int, []byte) {
		req, _ := http.NewRequest(method, "https://127.0.0.1:"+o.apiPort+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer beeguard-token")
		resp, err := client.Do(req)
		if err != nil {
			return 0, []byte(err.Error())
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, data
	}
	var active []api.Alarm
	eventually(t, "the API", logs, func() bool {
		status, body := call("GET", "/v1/alarms/active", "")
		return status == 200 && json.Unmarshal(body, &active) == nil && len(active) == 2
	})
	status, body := call("POST", "/v1/alarms/Guard1.TempHigh/commands", `{"command":"ack","user":"franklin"}`)
	if status != 200 {
		t.Fatalf("ack: %d %s", status, body)
	}
	acks, _ := reader.Query(ctx, journal.Filter{Kinds: []alarm.EventKind{alarm.Acknowledged}})
	if len(acks) != 1 || acks[0].User != "franklin" {
		t.Fatalf("journal acks = %+v", acks)
	}

	// A pulse far shorter than any poll still reaches the journal through the
	// change feed: close, open and close the lid within microseconds.
	for _, v := range []plc.BOOL{false, true, false} {
		if err := field.SetTagValue("Guard1.Lid", v); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "the short pulse in the journal", logs, func() bool {
		events, _ := reader.Query(ctx, journal.Filter{Alarm: "Guard1.LidOpen", Kinds: []alarm.EventKind{alarm.Activated}})
		return len(events) == 2
	})

	// The field database goes away: the linked tags turn bad quality.
	stopField()
	if err := field.SetTagValue("Guard1.Lid", plc.BOOL(false)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the bridge to report the unreachable field database", logs, func() bool {
		return strings.Contains(logs.String(), "tag bridge")
	})

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v; log:\n%s", err, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestRunConfigErrors(t *testing.T) {
	dir := t.TempDir()
	links := filepath.Join(dir, "links.json")
	alarms := filepath.Join(dir, "alarms.json")
	_ = os.WriteFile(links, []byte(`{"databases": []}`), 0o600)
	_ = os.WriteFile(alarms, []byte(`{"alarms": []}`), 0o600)
	base := options{linksPath: links, alarmsPath: alarms, logFormat: "text", logLevel: "info"}

	cases := map[string]func(o *options){
		"bad log level":    func(o *options) { o.logLevel = "loud" },
		"bad log format":   func(o *options) { o.logFormat = "xml" },
		"missing links":    func(o *options) { o.linksPath = filepath.Join(dir, "missing.json") },
		"no token":         func(o *options) { o.apiPort = "1" },
		"no certificate":   func(o *options) { o.apiPort = "1"; o.token = "t" },
		"unknown journal":  func(o *options) { o.journalPath = filepath.Join(dir, "journal.csv") },
		"sqlite not built": func(o *options) { o.journalPath = filepath.Join(dir, "journal.db") },
	}
	for name, change := range cases {
		o := base
		change(&o)
		if name == "sqlite not built" {
			if _, ok := openers[".db"]; ok {
				continue // built with -tags sqlite
			}
		}
		if err := run(context.Background(), o, &syncBuffer{}); err == nil {
			t.Errorf("%s: run accepted it", name)
		}
	}
}
