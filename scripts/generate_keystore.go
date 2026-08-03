package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	"golang.org/x/crypto/hkdf"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: go run generate_keystore.go <master_seed> <context>")
		os.Exit(1)
	}
	seed := os.Args[1]
	context := os.Args[2]

	hkdfReader := hkdf.New(sha256.New, []byte(seed), []byte("syncthing-luks"), []byte(context))
	key := make([]byte, 32)
	io.ReadFull(hkdfReader, key)

	// Construct ECDSA P-256 key deterministically
	d := new(big.Int).SetBytes(key)
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
		},
		D: d,
	}
	priv.PublicKey.X, priv.PublicKey.Y = priv.PublicKey.Curve.ScalarBaseMult(key)

	// Provide a dummy random reader for blinding during signature generation
	// (CreateCertificate might still use it for blinding, but the key itself is deterministic)
	dummyRand := rand.Reader

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Syncthing LUKS " + context,
			Organization: []string{"Syncthing LUKS"},
		},
		NotBefore:             time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(dummyRand, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}

	// Write Private Key
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		panic(err)
	}
	keyOut, _ := os.Create("key_" + context + ".pem")
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	keyOut.Close()

	// Write Cert
	certOut, _ := os.Create("cert_" + context + ".pem")
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()
}
