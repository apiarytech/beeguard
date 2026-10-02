/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package link

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/apiarytech/honeycomb"
	plc "github.com/apiarytech/royaljelly/iec"

	"github.com/apiarytech/beeguard/internal/devcert"
)

func TestParse(t *testing.T) {
	cfg, err := Parse(strings.NewReader(`{"databases": [{
		"id": "field1", "url": "https://field-pc:8443", "token": "t", "timeout": "2s",
		"tags": ["Guard1.Temp", {"tag": "Guard1.Lid", "remote": "Hive1.LidSwitch"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Databases[0]
	if d.ID != "field1" || time.Duration(d.Timeout) != 2*time.Second || len(d.Tags) != 2 ||
		d.Tags[0] != (Tag{Tag: "Guard1.Temp"}) || d.Tags[1] != (Tag{Tag: "Guard1.Lid", Remote: "Hive1.LidSwitch"}) {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestParseRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"http":            `{"databases": [{"id": "a", "url": "http://x:1"}]}`,
		"no id":           `{"databases": [{"url": "https://x:1"}]}`,
		"duplicate db":    `{"databases": [{"id": "a", "url": "https://x"}, {"id": "a", "url": "https://y"}]}`,
		"duplicate tag":   `{"databases": [{"id": "a", "url": "https://x", "tags": ["T"]}, {"id": "b", "url": "https://y", "tags": ["T"]}]}`,
		"both tokens":     `{"databases": [{"id": "a", "url": "https://x", "token": "t", "tokenEnv": "E"}]}`,
		"unknown key":     `{"databases": [{"id": "a", "url": "https://x", "tag": ["T"]}]}`,
		"unknown tag key": `{"databases": [{"id": "a", "url": "https://x", "tags": [{"name": "T"}]}]}`,
		"bad timeout":     `{"databases": [{"id": "a", "url": "https://x", "timeout": "soon"}]}`,
	} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokenFromEnvironment(t *testing.T) {
	t.Setenv("BEEGUARD_TEST_TOKEN", "from-env")
	c, err := Database{ID: "a", URL: "https://x", TokenEnv: "BEEGUARD_TEST_TOKEN"}.client()
	if err != nil || c.BearerToken != "from-env" {
		t.Fatalf("client = %+v, %v", c, err)
	}
	if _, err := (Database{ID: "a", URL: "https://x", TokenEnv: "BEEGUARD_TEST_UNSET"}).client(); err == nil {
		t.Fatal("accepted an empty token variable")
	}
	if _, err := (Database{ID: "a", URL: "https://x", CAFile: "missing.pem"}).client(); err == nil {
		t.Fatal("accepted a missing CA file")
	}
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

// TestApplyOverHTTPS links to a real honeycomb server with verified TLS.
func TestApplyOverHTTPS(t *testing.T) {
	certFile, keyFile, err := devcert.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	field := honeycomb.NewTagDatabase()
	if err := field.AddTag(&honeycomb.Tag{Name: "Hive1.LidSwitch", TypeInfo: &honeycomb.TypeInfo{DataType: honeycomb.TypeBOOL}, Value: plc.BOOL(false)}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	if err := field.SetTagValueQualityAt("Hive1.LidSwitch", plc.BOOL(true), honeycomb.QualityGood, stamp); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	honeycomb.StartServer(field, []string{"secret"}, port, certFile, keyFile, nil, ctx)

	local := honeycomb.NewTagDatabase()
	cfg := Config{Databases: []Database{{
		ID: "field1", URL: "https://127.0.0.1:" + port, Token: "secret", CAFile: certFile,
		Tags: []Tag{{Tag: "Guard1.Lid", Remote: "Hive1.LidSwitch"}},
	}}}
	if err := cfg.Apply(local); err != nil {
		t.Fatal(err)
	}

	var r honeycomb.Reading
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r, err = local.ReadTag("Guard1.Lid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ReadTag: %v", err)
		}
		time.Sleep(20 * time.Millisecond) // the server starts listening asynchronously
	}
	if r.Value != true || r.Quality != honeycomb.QualityGood || !r.Timestamp.Equal(stamp) {
		t.Fatalf("reading = %+v", r)
	}

	// A wrong token is refused.
	bad := honeycomb.NewTagDatabase()
	cfg.Databases[0].Token = "wrong"
	if err := cfg.Apply(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.ReadTag("Guard1.Lid"); err == nil {
		t.Fatal("a wrong token was accepted")
	}
}
