/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apiarytech/beeguard/alarm"
	"github.com/apiarytech/beeguard/api"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/evaluator"
	"github.com/apiarytech/beeguard/journal"
)

func newServer(t *testing.T) (*httptest.Server, *engine.Engine) {
	t.Helper()
	j := journal.NewMemory(0)
	eng, err := engine.New([]engine.Definition{{
		Alarm:     alarm.Config{ID: "Guard1.TempHigh", Severity: 700, AckRequired: true, MaxShelve: time.Hour},
		Source:    "Guard1.Temp",
		Condition: evaluator.Config{Kind: evaluator.High, Limit: 38},
	}}, engine.WithSink(j))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(New(api.New(eng, j), []string{"secret"}))
	t.Cleanup(srv.Close)
	return srv, eng
}

func do(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAuth(t *testing.T) {
	srv, _ := newServer(t)
	for _, token := range []string{"", "wrong"} {
		if status, _ := do(t, srv, "GET", "/v1/alarms", token, ""); status != http.StatusUnauthorized {
			t.Errorf("token %q: status %d", token, status)
		}
	}
}

func TestAlarmsAndCommands(t *testing.T) {
	srv, eng := newServer(t)
	eng.Process("Guard1.Temp", evaluator.Sample{Value: 39, Good: true})

	status, body := do(t, srv, "GET", "/v1/alarms/active", "secret", "")
	var active []api.Alarm
	if err := json.Unmarshal(body, &active); err != nil || status != 200 || len(active) != 1 || active[0].State != "ACTIVE_UNACK" {
		t.Fatalf("active: %d %s", status, body)
	}

	status, body = do(t, srv, "POST", "/v1/alarms/Guard1.TempHigh/commands", "secret", `{"command":"ack","user":"franklin"}`)
	var a api.Alarm
	if err := json.Unmarshal(body, &a); err != nil || status != 200 || !a.Acked {
		t.Fatalf("ack: %d %s", status, body)
	}

	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1/alarms/Guard1.TempHigh/commands", `{"command":"ack","user":"franklin"}`, http.StatusConflict},
		{"POST", "/v1/alarms/Nope/commands", `{"command":"ack","user":"franklin"}`, http.StatusNotFound},
		{"POST", "/v1/alarms/Guard1.TempHigh/commands", `{"command":"ack"}`, http.StatusBadRequest},
		{"POST", "/v1/alarms/Guard1.TempHigh/commands", `{"cmd":"ack"}`, http.StatusBadRequest},
		{"GET", "/v1/alarms/Nope", "", http.StatusNotFound},
		{"GET", "/v1/alarms", "", http.StatusOK},
	}
	for _, c := range cases {
		if status, body := do(t, srv, c.method, c.path, "secret", c.body); status != c.want {
			t.Errorf("%s %s %s: %d %s, want %d", c.method, c.path, c.body, status, body, c.want)
		}
	}
	status, body = do(t, srv, "POST", "/v1/alarms/Guard1.TempHigh/commands", "secret", `{"command":"ack","user":"franklin"}`)
	var e api.Error
	if json.Unmarshal(body, &e) != nil || e.Code != api.CodeRejected || e.Message == "" {
		t.Fatalf("error body: %d %s", status, body)
	}
}

func TestEvents(t *testing.T) {
	srv, eng := newServer(t)
	eng.Process("Guard1.Temp", evaluator.Sample{Value: 39, Good: true})
	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)

	status, body := do(t, srv, "GET", "/v1/events?kind=ACTIVATED&limit=5&since="+since, "secret", "")
	var events []api.Event
	if err := json.Unmarshal(body, &events); err != nil || status != 200 || len(events) != 1 || events[0].Kind != "ACTIVATED" {
		t.Fatalf("events: %d %s", status, body)
	}
	for _, q := range []string{"limit=x", "since=yesterday", "kind=EXPLODED"} {
		if status, _ := do(t, srv, "GET", "/v1/events?"+q, "secret", ""); status != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, status)
		}
	}
}
