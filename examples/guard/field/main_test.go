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
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apiarytech/honeycomb"
	"github.com/apiarytech/honeycomb/connectors/plc4x"

	"github.com/apiarytech/beeguard/internal/devcert"
	"github.com/apiarytech/beeguard/internal/modbus_sim"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
}

// TestServesDeviceValues polls the Modbus simulator through the PLC4X connector
// and reads the values back over the HTTPS tag API, the way beeguard does.
func TestServesDeviceValues(t *testing.T) {
	sim := modbussim.New(16)
	sim.SetFloat32(modbussim.TempAddr, 39)
	sim.SetCoil(modbussim.LidAddr, true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sim.Serve(ctx, ln) }()

	dir := t.TempDir()
	certFile, _, err := devcert.Ensure(filepath.Join(dir, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	ioPath := filepath.Join(dir, "io.json")
	_ = os.WriteFile(ioPath, []byte(fmt.Sprintf(`{"connections": [{"name": "guard1", "url": "modbus-tcp://%s", "interval": "100ms",
		"bindings": [{"tag": "Guard1.Temp", "address": "holding-register:1:REAL"}, {"tag": "Guard1.Lid", "address": "coil:1:BOOL"}]}]}`,
		ln.Addr())), 0o600)
	o := options{ioPath: ioPath, port: freePort(t), devCert: filepath.Join(dir, "dev"), token: "secret", plc4xLog: "disabled"}
	done := make(chan error)
	go func() { done <- run(ctx, o, io.Discard) }()

	pem, _ := os.ReadFile(certFile)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	client := &honeycomb.NetworkDatabaseClient{
		RemoteAddress: "https://127.0.0.1:" + o.port,
		BearerToken:   "secret",
		Client:        &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
	}
	local := honeycomb.NewTagDatabase()
	_ = local.RegisterDatabase("field", client)
	_ = local.AddTag(&honeycomb.Tag{Name: "Temp", RemoteAlias: &honeycomb.RemoteAliasInfo{DBID: "field", TagName: "Guard1.Temp"}})

	deadline := time.Now().Add(15 * time.Second)
	for {
		r, err := local.ReadTag("Temp")
		if err == nil && r.Value == 39.0 && r.Quality == honeycomb.QualityGood && !r.Timestamp.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("last reading %+v, %v", r, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestCreateTags(t *testing.T) {
	db := honeycomb.NewTagDatabase()
	cfg := plc4x.Config{Connections: []plc4x.Connection{{Name: "c", Bindings: []plc4x.Binding{
		{Tag: "A", Address: "holding-register:1:REAL"},
		{Tag: "B", Address: "coil:1:bool"},
	}}}}
	if err := createTags(db, cfg); err != nil {
		t.Fatal(err)
	}
	if err := createTags(db, cfg); err != nil {
		t.Fatalf("existing tags must be kept: %v", err)
	}
	if a, _ := db.GetTag("A"); a.TypeInfo.DataType != honeycomb.TypeREAL {
		t.Fatalf("A has type %v", a.TypeInfo.DataType)
	}
	bad := plc4x.Config{Connections: []plc4x.Connection{{Bindings: []plc4x.Binding{{Tag: "C", Address: "holding-register:1:REAL[4]"}}}}}
	if err := createTags(db, bad); err == nil {
		t.Fatal("created a tag for an array address")
	}
}

func TestRunNeedsTokenAndCertificate(t *testing.T) {
	if err := run(context.Background(), options{plc4xLog: "error"}, io.Discard); err == nil {
		t.Fatal("ran without a token")
	}
	if err := run(context.Background(), options{plc4xLog: "error", token: "t"}, io.Discard); err == nil {
		t.Fatal("ran without a certificate")
	}
}
