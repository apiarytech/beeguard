/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package httpapi serves an api.Service as JSON over HTTP, using only the
// standard library. Every request needs "Authorization: Bearer <token>".
//
//	GET  /v1/alarms                    every alarm
//	GET  /v1/alarms/active             alarms in alarm or waiting for ack
//	GET  /v1/alarms/{id}               one alarm
//	POST /v1/alarms/{id}/commands      {"command": "ack", "user": "franklin"}
//	GET  /v1/events?alarm=&kind=&since=&until=&limit=
//
// "since" and "until" are RFC 3339 times; "kind" may repeat. Errors are
// {"code": "...", "message": "..."} with the HTTP status from the api.Code.
// Serve it over TLS (http.Server.ListenAndServeTLS).
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apiarytech/beeguard/api"
)

const maxBody = 64 << 10

// New returns a handler for svc that accepts any of tokens.
func New(svc api.Service, tokens []string) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alarms", h.list)
	mux.HandleFunc("GET /v1/alarms/active", h.active)
	mux.HandleFunc("GET /v1/alarms/{id}", h.get)
	mux.HandleFunc("POST /v1/alarms/{id}/commands", h.command)
	mux.HandleFunc("GET /v1/events", h.events)
	return auth(tokens, mux)
}

type handler struct{ svc api.Service }

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	alarms, err := h.svc.ListAlarms(r.Context())
	reply(w, alarms, err)
}

func (h *handler) active(w http.ResponseWriter, r *http.Request) {
	alarms, err := h.svc.ActiveAlarms(r.Context())
	reply(w, alarms, err)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.GetAlarm(r.Context(), r.PathValue("id"))
	reply(w, a, err)
}

func (h *handler) command(w http.ResponseWriter, r *http.Request) {
	var req api.CommandRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		reply(w, nil, &api.Error{Code: api.CodeInvalid, Message: "body: " + err.Error()})
		return
	}
	req.Alarm = r.PathValue("id")
	a, err := h.svc.Command(r.Context(), req)
	reply(w, a, err)
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := api.EventQuery{Alarm: q.Get("alarm"), Kinds: q["kind"]}
	var err error
	if v := q.Get("limit"); v != "" {
		if query.Limit, err = strconv.Atoi(v); err != nil {
			reply(w, nil, &api.Error{Code: api.CodeInvalid, Message: "limit: " + err.Error()})
			return
		}
	}
	for name, dst := range map[string]*time.Time{"since": &query.Since, "until": &query.Until} {
		if v := q.Get(name); v != "" {
			if *dst, err = time.Parse(time.RFC3339Nano, v); err != nil {
				reply(w, nil, &api.Error{Code: api.CodeInvalid, Message: name + ": " + err.Error()})
				return
			}
		}
	}
	events, err := h.svc.QueryEvents(r.Context(), query)
	reply(w, events, err)
}

var statusOf = map[api.Code]int{
	api.CodeNotFound:        http.StatusNotFound,
	api.CodeInvalid:         http.StatusBadRequest,
	api.CodeRejected:        http.StatusConflict,
	api.CodeUnavailable:     http.StatusServiceUnavailable,
	api.CodeUnauthenticated: http.StatusUnauthorized,
	api.CodeInternal:        http.StatusInternalServerError,
}

func reply(w http.ResponseWriter, body any, err error) {
	status := http.StatusOK
	if err != nil {
		code := api.CodeOf(err)
		status = statusOf[code]
		body = api.Error{Code: code, Message: strings.TrimPrefix(err.Error(), string(code)+": ")}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func auth(tokens []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && given != "" {
			for _, t := range tokens {
				if subtle.ConstantTimeCompare([]byte(given), []byte(t)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="beeguard"`)
		reply(w, nil, &api.Error{Code: api.CodeUnauthenticated, Message: "a valid bearer token is required"})
	})
}
