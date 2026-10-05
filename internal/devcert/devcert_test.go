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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestConstrained: the development CA cannot vouch for other hosts, so a
// leaked key signs nothing a client would accept for another name.
func TestConstrained(t *testing.T) {
	certFile, keyFile, err := Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(pair.Certificate[0])
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	sign := func(dns string) *x509.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: dns}, DNSNames: []string{dns},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, pair.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		return c
	}
	if _, err := sign("evil.example").Verify(x509.VerifyOptions{Roots: roots, DNSName: "evil.example"}); err == nil {
		t.Fatal("a certificate for evil.example signed with the development key verifies")
	}
	if _, err := sign("localhost").Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost"}); err != nil {
		t.Fatalf("localhost: %v", err)
	}
}

// TestReplacesUnconstrained: a certificate made by an older version is
// replaced.
func TestReplacesUnconstrained(t *testing.T) {
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now(), NotAfter: time.Now().AddDate(1, 0, 0), DNSNames: []string{"localhost"}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(filepath.Join(dir, "key.pem"), []byte("old"), 0o600)
	certFile, _, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !usable(certFile) {
		t.Fatal("the unconstrained certificate was kept")
	}
}
