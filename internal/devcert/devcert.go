/*
 * Copyright (C) 2026 Franklin D. Amador
 *
 * This software is dual-licensed under the terms of the GPL v3.0 and
 * a commercial license. You may choose to use this software under either
 * license.
 *
 * See the LICENSE files in the project root for full license text.
 */

// Package devcert creates a self-signed TLS certificate for development and
// tests, so the HTTPS APIs can run with certificate verification switched on.
// Do not use it in production: use certificates from your own CA.
package devcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Ensure returns cert.pem and key.pem in dir, creating them if either is
// missing, the certificate is unconstrained (made by an older version) or it
// expires within 30 days. The certificate is valid for one year for
// localhost, 127.0.0.1, ::1 and the machine's host name, and can certify
// nothing else. Clients trust it by using cert.pem as their CA file.
func Ensure(dir string) (certFile, keyFile string, err error) {
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if usable(certFile) && exists(keyFile) {
		return certFile, keyFile, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("devcert: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("devcert: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("devcert: %w", err)
	}
	names := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" {
		names = append(names, host)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "beeguard development", Organization: []string{"beeguard"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // self-signed: it is its own CA, so clients can trust it directly
		// A constrained CA: it can certify only its own names and the
		// loopback addresses, and no intermediate CA, so its key cannot
		// vouch for any other host even if it leaks.
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         names,
		PermittedIPRanges: []*net.IPNet{
			{IP: net.IPv4(127, 0, 0, 1).To4(), Mask: net.CIDRMask(32, 32)},
			{IP: net.IPv6loopback, Mask: net.CIDRMask(128, 128)},
		},
		DNSNames:    names,
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("devcert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("devcert: %w", err)
	}
	if err := writePEM(keyFile, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return "", "", err
	}
	if err := writePEM(certFile, "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// usable reports whether certFile is a constrained development CA that is
// valid for at least another 30 days.
func usable(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return cert.PermittedDNSDomainsCritical && cert.MaxPathLenZero && time.Now().Add(30*24*time.Hour).Before(cert.NotAfter)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("devcert: %w", err)
	}
	err = pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
	return errors.Join(err, f.Close())
}
