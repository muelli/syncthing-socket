// SPDX-License-Identifier: AGPL-3.0-or-later

// Package identity derives syncthing-socket's deterministic TLS identities.
//
// Both ends of a connection derive certificates from a shared seed, so each can compute
// the other's Syncthing Device ID without exchanging anything. It imports only the
// standard library.
package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// Certificate seed suffixes. The two ends of a connection derive different identities from
// the same passphrase so that a peer cannot be reflected back onto itself; every derivation
// must go through one of these.
const (
	CertSuffixServer = "server"
	CertSuffixClient = "client"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// GenerateDeterministicCert returns a self-signed Ed25519 certificate derived from seed
// alone. The same seed always yields byte-identical certificates. Append CertSuffixServer
// or CertSuffixClient to the shared seed to get the identity of that end.
func GenerateDeterministicCert(seed string) (tls.Certificate, error) {
	h := sha256.Sum256([]byte(seed))
	priv := ed25519.NewKeyFromSeed(h[:])

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "syncthing-socket",
		},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Unix(0, 0).AddDate(100, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	var zr zeroReader
	derBytes, err := x509.CreateCertificate(zr, &template, &template, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}, nil
}
