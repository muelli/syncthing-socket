// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"testing"
)

func generate(t *testing.T, seed string) []byte {
	t.Helper()
	cert, err := GenerateDeterministicCert(seed)
	if err != nil {
		t.Fatalf("GenerateDeterministicCert(%q): %v", seed, err)
	}
	if len(cert.Certificate) != 1 || cert.PrivateKey == nil {
		t.Fatalf("GenerateDeterministicCert(%q): want one certificate and a private key", seed)
	}
	return cert.Certificate[0]
}

func TestSameSeedSameCertificate(t *testing.T) {
	a := generate(t, "example-seed")
	b := generate(t, "example-seed")
	if !bytes.Equal(a, b) {
		t.Fatal("the same seed produced two different certificates")
	}
}

func TestDifferentSeedsDiffer(t *testing.T) {
	if bytes.Equal(generate(t, "example-seed"), generate(t, "another-seed")) {
		t.Fatal("different seeds produced the same certificate")
	}
}

func TestSuffixesGiveDifferentIdentities(t *testing.T) {
	server := generate(t, "example-seed"+CertSuffixServer)
	client := generate(t, "example-seed"+CertSuffixClient)
	plain := generate(t, "example-seed")
	if bytes.Equal(server, client) || bytes.Equal(plain, server) || bytes.Equal(plain, client) {
		t.Fatal("server, client, and unsuffixed seeds must give three distinct certificates")
	}
	if CertSuffixServer != "server" || CertSuffixClient != "client" {
		t.Fatalf("suffixes changed to %q and %q; every enrolled peer would get a new identity",
			CertSuffixServer, CertSuffixClient)
	}
}

// Both ends derive the other's identity from the seed alone, on different machines and
// different releases, so the bytes must never change. These were recorded from the
// implementation as it stood in the root package before the move.
func TestCertificateIsStableAcrossReleases(t *testing.T) {
	for seed, want := range map[string]string{
		"example-seed" + CertSuffixServer: "19b1d7ee5f1bb3cee55f5818a3e5c79d8b50dfc4a41ba0d8d053f39835d5b44e",
		"example-seed" + CertSuffixClient: "cc9933d08fded30c2dd410a1cde6930ff3ddb84636cb0a2ac05af6b572e1cb83",
	} {
		sum := sha256.Sum256(generate(t, seed))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("SHA-256 of the certificate for %q = %s, want %s", seed, got, want)
		}
	}
}

func TestCertificateServesClientAndServerAuth(t *testing.T) {
	cert, err := x509.ParseCertificate(generate(t, "example-seed"+CertSuffixServer))
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		opts := x509.VerifyOptions{
			Roots:       roots,
			CurrentTime: cert.NotBefore.AddDate(1, 0, 0),
			KeyUsages:   []x509.ExtKeyUsage{usage},
		}
		if _, err := cert.Verify(opts); err != nil {
			t.Errorf("certificate does not verify for usage %v: %v", usage, err)
		}
	}
}
