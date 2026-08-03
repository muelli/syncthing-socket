package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
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

type cipherReader struct {
	stream cipher.Stream
}

func (c *cipherReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = 0
	}
	c.stream.XORKeyStream(p, p)
	return len(p), nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: go run generate_keystore.go <master_seed> <context>")
		os.Exit(1)
	}
	seed := os.Args[1]
	context := os.Args[2]

	// Use HKDF to derive a 32-byte AES key and 16-byte nonce from the seed
	hkdfReader := hkdf.New(sha256.New, []byte(seed), []byte("syncthing-luks"), []byte(context))
	key := make([]byte, 32)
	nonce := make([]byte, 16)
	io.ReadFull(hkdfReader, key)
	io.ReadFull(hkdfReader, nonce)

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	// Create an infinite stream of zeros, encrypted with AES-CTR
	stream := cipher.NewCTR(block, nonce)
	randReader := &cipherReader{stream: stream}

	// Generate RSA 2048 key
	priv, err := rsa.GenerateKey(randReader, 2048)
	if err != nil {
		panic(err)
	}

	// Create a self-signed certificate
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

	derBytes, err := x509.CreateCertificate(randReader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}

	// Write Private Key
	keyOut, _ := os.Create("key_" + context + ".pem")
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()

	// Write Cert
	certOut, _ := os.Create("cert_" + context + ".pem")
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()
}
