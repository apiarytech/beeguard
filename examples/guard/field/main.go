/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Command field is the field side of the guard example: a honeycomb tag
// database fed from devices by the PLC4X connector and served over honeycomb's
// HTTPS tag API, for beeguard to link to.
//
// It is a separate Go module so that beeguard itself does not depend on
// PLC4X. Any program that serves a honeycomb TagDatabase can take its place.
//
//	go run . -io ../io.json -dev-cert ../dev -token field-secret
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/apiarytech/honeycomb"
	"github.com/apiarytech/honeycomb/connectors/plc4x"
	"github.com/rs/zerolog"

	"github.com/apiarytech/beeguard/internal/devcert"
)

type options struct {
	ioPath, port              string
	cert, key, devCert, token string
	tokenEnv, plc4xLog        string
}

func main() {
	var o options
	flag.StringVar(&o.ioPath, "io", "io.json", "PLC4X connector configuration (honeycomb plc4x JSON)")
	flag.StringVar(&o.port, "port", "8443", "HTTPS port of the honeycomb tag API")
	flag.StringVar(&o.cert, "cert", "", "TLS certificate")
	flag.StringVar(&o.key, "key", "", "TLS private key")
	flag.StringVar(&o.devCert, "dev-cert", "", "directory for a self-signed development certificate, used when -cert is empty")
	flag.StringVar(&o.token, "token", "", "bearer token clients must send")
	flag.StringVar(&o.tokenEnv, "token-env", "FIELD_TOKEN", "environment variable read when -token is empty")
	flag.StringVar(&o.plc4xLog, "plc4x-log", "error", "PLC4X driver log level: trace, debug, info, warn, error or disabled")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "field:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options, logOut io.Writer) error {
	logger := slog.New(slog.NewTextHandler(logOut, nil))
	// PLC4X logs through zerolog's global logger, which by default writes
	// trace-level JSON; connection errors reach us through the connector anyway.
	level, err := zerolog.ParseLevel(o.plc4xLog)
	if err != nil {
		return fmt.Errorf("-plc4x-log: %w", err)
	}
	zerolog.SetGlobalLevel(level)

	token := o.token
	if token == "" {
		token = os.Getenv(o.tokenEnv)
	}
	if token == "" {
		return fmt.Errorf("set -token or %s", o.tokenEnv)
	}
	certFile, keyFile := o.cert, o.key
	if certFile == "" && o.devCert != "" {
		if certFile, keyFile, err = devcert.Ensure(o.devCert); err != nil {
			return err
		}
		logger.Warn("using a self-signed development certificate", "cert", certFile)
	}
	if certFile == "" || keyFile == "" {
		return errors.New("set -cert and -key, or -dev-cert DIR")
	}

	cfg, err := plc4x.LoadConfig(o.ioPath)
	if err != nil {
		return err
	}
	db := honeycomb.NewTagDatabase()
	if err := createTags(db, cfg); err != nil {
		return err
	}
	connector, err := plc4x.NewFromConfig(db, cfg, plc4x.WithErrorHandler(func(conn string, err error) {
		logger.Warn("device", "connection", conn, "err", err)
	}))
	if err != nil {
		return err
	}
	honeycomb.StartServer(db, []string{token}, o.port, certFile, keyFile, nil, ctx)
	logger.Info("serving", "tags", len(db.GetAllTagNames()), "port", o.port)
	return connector.Run(ctx)
}

// createTags adds a tag for every binding that does not have one, typed from
// the data type at the end of its PLC4X address, e.g. holding-register:1:REAL.
func createTags(db *honeycomb.TagDatabase, cfg plc4x.Config) error {
	for _, conn := range cfg.Connections {
		for _, b := range conn.Bindings {
			if _, ok := db.GetTag(b.Tag); ok {
				continue
			}
			i := strings.LastIndex(b.Address, ":")
			dataType := honeycomb.DataType(strings.ToUpper(b.Address[i+1:]))
			value, err := honeycomb.NewValueFromDataType(dataType)
			if err != nil || strings.ContainsAny(string(dataType), "[]") {
				return fmt.Errorf("tag %s: cannot create a tag for address %q; use a scalar type such as REAL or BOOL", b.Tag, b.Address)
			}
			if err := db.AddTag(&honeycomb.Tag{
				Name:        b.Tag,
				Description: conn.Name + " " + b.Address,
				TypeInfo:    &honeycomb.TypeInfo{DataType: dataType},
				Value:       honeycomb.Dereference(value), // the connector converts into the plain IEC type
			}); err != nil {
				return fmt.Errorf("tag %s: %w", b.Tag, err)
			}
		}
	}
	return nil
}
