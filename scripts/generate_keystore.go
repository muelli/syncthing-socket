// Command generate_keystore derives a code-signing identity from the single master seed.
//
// Every signing identity this project uses comes from one secret, MASTER_SECRET, plus a
// role label. The seed is the only secret state; everything else is deterministic, public,
// or re-derivable.
//
// Usage:
//
//	generate_keystore <seed> <role>              verify against the pinned certificate
//	generate_keystore -bootstrap <seed> <role>   mint the certificate for the first time
//
// Normal mode writes key_<role>.pem and cert_<role>.pem into the working directory, where
// cert_<role>.pem is a copy of the pinned signing/<role>-cert.pem. It hard-fails when the
// seed does not reproduce the pinned certificate's public key, which turns a wrong or
// missing secret into an obvious build failure rather than a release nobody can upgrade
// across.
//
// The pinning is not optional, and it cannot be replaced by deriving the certificate too.
// Android identifies a signer by the exact certificate bytes and an F-Droid repository
// fingerprint is the SHA-256 of the index-signing certificate, so a certificate that
// changes is a new identity. ECDSA self-signatures are randomised, and Go deliberately
// prevents reproducing them: crypto/ecdsa calls randutil.MaybeReadByte, which consumes
// zero or one bytes from the caller's reader depending on Go's own global random source,
// specifically so that nobody can depend on deterministic signing. Feeding it a fixed
// stream therefore still yields a different certificate each run, which was measured
// rather than assumed. Hence: mint once, commit the result, verify against it forever.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/hkdf"
)

// hkdfSalt is part of the derivation contract. Changing it, or any info string below,
// changes every identity and is an app-identity reset.
const hkdfSalt = "syncthing-luks"

// certNotBefore and certNotAfter are fixed so that a certificate minted from a given seed
// does not depend on when it was minted.
var (
	certNotBefore = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	certNotAfter  = time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC)
)

// expand returns len bytes of the HKDF stream for one role and purpose.
func expand(seed, role, purpose string, length int) ([]byte, error) {
	r := hkdf.New(sha256.New, []byte(seed), []byte(hkdfSalt), []byte(role+purpose))
	out := make([]byte, length)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// deriveECKey derives the P-256 signing key for a role.
//
// The scalar must lie in [1, n-1]. Reducing an arbitrary 32-byte string modulo n-1 and
// adding one is the standard way to get there; using the raw bytes as the scalar, as this
// once did, is out of range with negligible but nonzero probability and is simply not what
// the curve parameters allow.
func deriveECKey(seed, role string) (*ecdsa.PrivateKey, error) {
	okm, err := expand(seed, role, "", 32)
	if err != nil {
		return nil, err
	}
	curve := elliptic.P256()
	n := curve.Params().N
	d := new(big.Int).SetBytes(okm)
	d.Mod(d, new(big.Int).Sub(n, big.NewInt(1)))
	d.Add(d, big.NewInt(1))

	priv := &ecdsa.PrivateKey{D: d}
	priv.Curve = curve
	priv.X, priv.Y = curve.ScalarBaseMult(d.Bytes())
	return priv, nil
}

// derivePassphrase returns the keystore passphrase for a role, so that no password has to
// be hardcoded in a workflow or stored as a second secret.
func derivePassphrase(seed, role string) (string, error) {
	prk, err := expand(seed, role, ":passphrase-key", 32)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, prk)
	mac.Write([]byte(role + ":passphrase"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:24]), nil
}

func certTemplate(role string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Syncthing LUKS " + role,
			Organization: []string{"Syncthing LUKS"},
		},
		NotBefore:             certNotBefore,
		NotAfter:              certNotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
}

// bootstrapCert mints the certificate for a role. Run once per role, ever: the result is
// this role's identity and re-running it produces a different, incompatible certificate.
func bootstrapCert(role string, priv *ecdsa.PrivateKey) ([]byte, error) {
	tmpl := certTemplate(role)
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
}

func pinnedCertPath(role string) string {
	return filepath.Join("signing", role+"-cert.pem")
}

func writePEM(path, blockType string, der []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "generate_keystore: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	args := os.Args[1:]
	bootstrap := false
	if len(args) > 0 && args[0] == "-bootstrap" {
		bootstrap = true
		args = args[1:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: generate_keystore [-bootstrap] <master_seed> <role>")
		os.Exit(2)
	}
	seed, role := args[0], args[1]
	if seed == "" {
		fail("the master seed is empty; refusing to produce a signing identity.\n" +
			"  Set the MASTER_SECRET repository secret. Falling back to a throwaway key would\n" +
			"  publish artefacts nobody can reproduce or upgrade across.")
	}

	priv, err := deriveECKey(seed, role)
	if err != nil {
		fail("deriving the %s key: %v", role, err)
	}

	var certDER []byte
	if bootstrap {
		if certDER, err = bootstrapCert(role, priv); err != nil {
			fail("minting the %s certificate: %v", role, err)
		}
		if err := os.MkdirAll("signing", 0o755); err != nil {
			fail("creating signing/: %v", err)
		}
		if err := writePEM(pinnedCertPath(role), "CERTIFICATE", certDER); err != nil {
			fail("writing %s: %v", pinnedCertPath(role), err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s; commit it, and never bootstrap this role again\n", pinnedCertPath(role))
	} else {
		pemBytes, err := os.ReadFile(pinnedCertPath(role))
		if err != nil {
			fail("reading the pinned certificate %s: %v\n"+
				"  Run with -bootstrap once to create it, then commit it.", pinnedCertPath(role), err)
		}
		block, _ := pem.Decode(pemBytes)
		if block == nil {
			fail("%s is not PEM", pinnedCertPath(role))
		}
		certDER = block.Bytes
		cert, err := x509.ParseCertificate(certDER)
		if err != nil {
			fail("parsing %s: %v", pinnedCertPath(role), err)
		}
		pinned, ok := cert.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			fail("%s does not hold an ECDSA public key", pinnedCertPath(role))
		}
		if !pinned.Equal(&priv.PublicKey) {
			fail("the seed does not match the committed certificate %s.\n"+
				"  Either MASTER_SECRET is wrong, or the certificate belongs to a different seed.\n"+
				"  Signing with a mismatched identity would break every existing install.",
				pinnedCertPath(role))
		}
	}

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		fail("marshalling the %s key: %v", role, err)
	}
	if err := writePEM("key_"+role+".pem", "EC PRIVATE KEY", privDER); err != nil {
		fail("writing key_%s.pem: %v", role, err)
	}
	if err := writePEM("cert_"+role+".pem", "CERTIFICATE", certDER); err != nil {
		fail("writing cert_%s.pem: %v", role, err)
	}

	pass, err := derivePassphrase(seed, role)
	if err != nil {
		fail("deriving the %s passphrase: %v", role, err)
	}
	// The passphrase is the only thing on stdout, so a workflow can capture it with
	// $(...) and mask it. Everything else goes to stderr.
	fmt.Println(pass)

	sum := sha256.Sum256(certDER)
	fmt.Fprintf(os.Stderr, "%s certificate sha256: %x\n", role, sum)
}
