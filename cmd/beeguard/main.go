/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Command beeguard is an alarm and event server for honeycomb tag databases.
//
// It links to one or more honeycomb databases over their HTTPS tag API
// (-links), evaluates the configured alarms on the linked tags (-alarms),
// journals every event (-journal), logs the events, and serves two HTTPS APIs:
//
//   - the honeycomb tag API (-tags-port), with each alarm's status and command
//     tags, so HMIs and other honeycomb databases can link to beeguard;
//   - the beeguard API (-api-port), see package api/httpapi.
//
// Both need -cert, -key and -token, or -dev-cert DIR to create a self-signed
// certificate for development.
//
//	beeguard -links links.json -alarms alarms.json -dev-cert dev -token secret
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apiarytech/honeycomb"

	"github.com/apiarytech/beeguard/api"
	"github.com/apiarytech/beeguard/api/httpapi"
	"github.com/apiarytech/beeguard/config"
	"github.com/apiarytech/beeguard/engine"
	"github.com/apiarytech/beeguard/internal/devcert"
	"github.com/apiarytech/beeguard/journal"
	"github.com/apiarytech/beeguard/journal/filejournal"
	"github.com/apiarytech/beeguard/link"
	"github.com/apiarytech/beeguard/sink/slogsink"
	"github.com/apiarytech/beeguard/tagbridge"
)

type options struct {
	linksPath, alarmsPath, journalPath string
	tick, poll, feedWait, skew         time.Duration
	user                               string
	tagsPort, apiPort                  string
	cert, key, devCert, token          string
	tokenEnv                           string
	logFormat, logLevel                string
}

func main() {
	var o options
	flag.StringVar(&o.linksPath, "links", "links.json", "linked honeycomb databases and tags")
	flag.StringVar(&o.alarmsPath, "alarms", "alarms.json", "alarm definitions")
	flag.StringVar(&o.journalPath, "journal", "beeguard.jsonl", "journal file: .jsonl, or .db/.sqlite in a build with -tags sqlite; empty keeps it in memory")
	flag.DurationVar(&o.tick, "tick", time.Second, "longest wait between evaluations; delays, shelves and rates are also evaluated exactly when due")
	flag.DurationVar(&o.poll, "poll", time.Second, "how often linked tags are read from a database without a change feed, and the retry interval")
	flag.DurationVar(&o.feedWait, "feed-wait", 25*time.Second, "how long one change-feed request waits before it is renewed")
	flag.DurationVar(&o.skew, "source-time-skew", 5*time.Second, "evaluate delays and rates in device time when it is within this of the server clock; 0 uses arrival time")
	flag.StringVar(&o.user, "user", "hmi", "user recorded for tag commands that name none")
	flag.StringVar(&o.tagsPort, "tags-port", "8443", "HTTPS port of the honeycomb tag API; empty disables it")
	flag.StringVar(&o.apiPort, "api-port", "8444", "HTTPS port of the beeguard API; empty disables it")
	flag.StringVar(&o.cert, "cert", "", "TLS certificate for both APIs")
	flag.StringVar(&o.key, "key", "", "TLS private key for both APIs")
	flag.StringVar(&o.devCert, "dev-cert", "", "directory for a self-signed development certificate, used when -cert is empty")
	flag.StringVar(&o.token, "token", "", "bearer token clients must send to both APIs")
	flag.StringVar(&o.tokenEnv, "token-env", "BEEGUARD_TOKEN", "environment variable read when -token is empty")
	flag.StringVar(&o.logFormat, "log-format", "text", "log format: text or json")
	flag.StringVar(&o.logLevel, "log-level", "info", "lowest level logged: debug, info, warn or error")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "beeguard:", err)
		os.Exit(1)
	}
}

func newLogger(o options, w io.Writer) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(o.logLevel)); err != nil {
		return nil, fmt.Errorf("-log-level: %w", err)
	}
	handlerOpts := &slog.HandlerOptions{Level: level}
	switch o.logFormat {
	case "text":
		return slog.New(slog.NewTextHandler(w, handlerOpts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, handlerOpts)), nil
	}
	return nil, fmt.Errorf("-log-format: %q is not text or json", o.logFormat)
}

// openers maps journal file extensions to journal constructors. The SQLite
// journal registers itself in builds with -tags sqlite (journal_sqlite.go).
var openers = map[string]func(ctx context.Context, path string) (journal.Journal, io.Closer, error){
	".jsonl": func(_ context.Context, path string) (journal.Journal, io.Closer, error) {
		j, err := filejournal.Open(path)
		return j, j, err
	},
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func openJournal(ctx context.Context, path string) (journal.Journal, io.Closer, error) {
	if path == "" {
		return journal.NewMemory(10000), nopCloser{}, nil
	}
	open, ok := openers[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil, fmt.Errorf("-journal %s: unsupported file type (use .jsonl; .db and .sqlite need a build with -tags sqlite)", path)
	}
	return open(ctx, path)
}

func run(ctx context.Context, o options, logOut io.Writer) error {
	logger, err := newLogger(o, logOut)
	if err != nil {
		return err
	}
	links, err := link.Load(o.linksPath)
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfig(o.alarmsPath)
	if err != nil {
		return err
	}
	defs := cfg.Alarms
	token := o.token
	if token == "" && o.tokenEnv != "" {
		token = os.Getenv(o.tokenEnv)
	}
	serving := o.tagsPort != "" || o.apiPort != ""
	certFile, keyFile := o.cert, o.key
	if serving {
		if token == "" {
			return fmt.Errorf("the APIs need a bearer token: set -token or %s", o.tokenEnv)
		}
		if certFile == "" && o.devCert != "" {
			if certFile, keyFile, err = devcert.Ensure(o.devCert); err != nil {
				return err
			}
			logger.Warn("using a self-signed development certificate", "cert", certFile)
		}
		if certFile == "" || keyFile == "" {
			return errors.New("the APIs need -cert and -key, or -dev-cert DIR")
		}
	}

	db := honeycomb.NewTagDatabase()
	if err := links.Apply(db); err != nil {
		return err
	}
	j, closer, err := openJournal(ctx, o.journalPath)
	if err != nil {
		return err
	}
	defer closer.Close()

	bridge := tagbridge.New(db, nil, tagbridge.WithUser(o.user), tagbridge.WithPollInterval(o.poll), tagbridge.WithFeedWait(o.feedWait),
		tagbridge.WithErrorHandler(func(err error) { logger.Warn("tag bridge", "err", err) }))
	eng, err := engine.New(defs,
		engine.WithSink(j), // journal first: the record is written before anything else reacts
		engine.WithSink(slogsink.New(logger)),
		engine.WithSink(bridge),
		engine.WithSourceTime(o.skew),
		engine.WithErrorHandler(func(err error) { logger.Error("sink", "err", err) }))
	if err != nil {
		return err
	}
	bridge.SetEngine(eng)
	if err := bridge.Setup(); err != nil {
		return err
	}

	var apiSrv *http.Server
	if o.apiPort != "" {
		if apiSrv, err = apiServer(o.apiPort, certFile, keyFile, httpapi.New(api.New(eng, j), []string{token})); err != nil {
			return err
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	start := func(fn func(context.Context) error) {
		wg.Go(func() {
			if err := fn(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
				cancel() // one part failed: stop the others
			}
		})
	}
	if o.tagsPort != "" {
		honeycomb.StartServer(db, []string{token}, o.tagsPort, certFile, keyFile, nil, runCtx)
	}
	if apiSrv != nil {
		start(func(ctx context.Context) error { return serve(ctx, apiSrv) })
	}
	start(bridge.Run)
	start(func(ctx context.Context) error { return eng.Run(ctx, o.tick) })
	if p, ok := j.(journal.Purger); ok && cfg.Journal.Retention > 0 {
		wg.Go(func() {
			journal.Retain(runCtx, p, cfg.Journal.Retention, time.Hour, func(err error) { logger.Warn("journal retention", "err", err) })
		})
	}

	logger.Info("started", "alarms", len(defs), "sources", strings.Join(eng.Sources(), ","),
		"journal", o.journalPath, "tags_port", o.tagsPort, "api_port", o.apiPort)
	wg.Wait()
	close(errs)
	logger.Info("stopped")
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return ctx.Err()
}

func apiServer(port, certFile, keyFile string, h http.Handler) (*http.Server, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	return &http.Server{
		Addr:      ":" + port,
		Handler:   h,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}},
		// Bounded like honeycomb's tag API: a slow or idle client cannot
		// hold a connection open, nor send unbounded headers.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}, nil
}

// serve runs srv until ctx is done, then shuts it down gracefully.
func serve(ctx context.Context, srv *http.Server) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServeTLS("", "") }()
	select {
	case err := <-errc:
		return fmt.Errorf("api: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	}
}
