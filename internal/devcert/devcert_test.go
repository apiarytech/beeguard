/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

package devcert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

func TestEnsure(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Fatal(err)
	}

	// A second call keeps the existing files.
	before, _ := os.ReadFile(certFile)
	if _, _, err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(certFile)
	if string(before) != string(after) {
		t.Fatal("Ensure replaced an existing certificate")
	}
}
