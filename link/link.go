/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package link connects beeguard's TagDatabase to other honeycomb databases.
// Each linked database is reached over honeycomb's HTTPS tag API, and each
// linked tag becomes a local remote-alias tag that alarms can use as a source.
//
// The JSON form is:
//
//	{
//	  "databases": [{
//	    "id": "field1",
//	    "url": "https://field-pc:8443",
//	    "tokenEnv": "FIELD1_TOKEN",
//	    "caFile": "certs/field1.pem",
//	    "timeout": "5s",
//	    "tags": ["Guard1.Temp", {"tag": "Guard1.Lid", "remote": "Hive1.LidSwitch"}]
//	  }]
//	}
//
// "url" must be https. "token" gives the bearer token directly; "tokenEnv"
// names an environment variable that holds it, which keeps secrets out of the
// file. "caFile" is a PEM certificate to trust in addition to the system roots,
// e.g. for a self-signed server. A tag is either a name, used locally and
// remotely, or an object whose "remote" name differs from the local "tag".
package link

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/apiarytech/honeycomb"
)

// Config lists the linked databases.
type Config struct {
	Databases []Database `json:"databases"`
}

// Database is one remote honeycomb database.
type Database struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Token    string   `json:"token"`
	TokenEnv string   `json:"tokenEnv"`
	CAFile   string   `json:"caFile"`
	Timeout  Duration `json:"timeout"`
	Tags     []Tag    `json:"tags"`
}

// Tag links a local tag name to a tag in the remote database.
type Tag struct {
	Tag    string `json:"tag"`
	Remote string `json:"remote"` // empty: same as Tag
}

// UnmarshalJSON accepts a plain name or an object.
func (t *Tag) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*t = Tag{Tag: name}
		return nil
	}
	type plain Tag
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode((*plain)(t))
}

// Duration is a time.Duration written as a Go duration string ("5s").
type Duration time.Duration

// UnmarshalJSON parses a Go duration string.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

// Load reads a Config from a JSON file.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("link: %w", err)
	}
	defer f.Close()
	cfg, err := Parse(f)
	if err != nil {
		return Config{}, fmt.Errorf("link %s: %w", path, err)
	}
	return cfg, nil
}

// Parse reads a Config. Unknown keys are rejected.
func Parse(r io.Reader) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

// Validate reports a configuration that cannot be applied.
func (c Config) Validate() error {
	ids := map[string]bool{}
	tags := map[string]bool{}
	for _, d := range c.Databases {
		if d.ID == "" {
			return errors.New("link: a database has no id")
		}
		if ids[d.ID] {
			return fmt.Errorf("link: duplicate database %q", d.ID)
		}
		ids[d.ID] = true
		u, err := url.Parse(d.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("link: database %q: url %q must be https://host[:port]", d.ID, d.URL)
		}
		if d.Token != "" && d.TokenEnv != "" {
			return fmt.Errorf("link: database %q: set token or tokenEnv, not both", d.ID)
		}
		for _, t := range d.Tags {
			if t.Tag == "" {
				return fmt.Errorf("link: database %q: a tag has no name", d.ID)
			}
			if tags[t.Tag] {
				return fmt.Errorf("link: tag %q is linked twice", t.Tag)
			}
			tags[t.Tag] = true
		}
	}
	return nil
}

// Apply registers each database with db and creates its linked tags.
func (c Config) Apply(db *honeycomb.TagDatabase) error {
	for _, d := range c.Databases {
		client, err := d.client()
		if err != nil {
			return err
		}
		if err := db.RegisterDatabase(d.ID, client); err != nil {
			return fmt.Errorf("link: %w", err)
		}
		for _, t := range d.Tags {
			remote := t.Remote
			if remote == "" {
				remote = t.Tag
			}
			err := db.AddTag(&honeycomb.Tag{
				Name:        t.Tag,
				Description: "linked to " + d.ID + ":" + remote,
				RemoteAlias: &honeycomb.RemoteAliasInfo{DBID: d.ID, TagName: remote},
			})
			if err != nil {
				return fmt.Errorf("link: tag %s: %w", t.Tag, err)
			}
		}
	}
	return nil
}

func (d Database) client() (*honeycomb.NetworkDatabaseClient, error) {
	token := d.Token
	if d.TokenEnv != "" {
		token = os.Getenv(d.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("link: database %q: environment variable %s is empty", d.ID, d.TokenEnv)
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if d.CAFile != "" {
		pem, err := os.ReadFile(d.CAFile)
		if err != nil {
			return nil, fmt.Errorf("link: database %q: %w", d.ID, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("link: database %q: no certificate in %s", d.ID, d.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	timeout := time.Duration(d.Timeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &honeycomb.NetworkDatabaseClient{
		RemoteAddress: d.URL,
		BearerToken:   token,
		Client: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsConfig, MaxIdleConnsPerHost: 4},
		},
	}, nil
}
