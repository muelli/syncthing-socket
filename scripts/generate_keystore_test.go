package main

import (
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"fmt"
	"math/big"
	"testing"
)

const testSeed = "a-fixed-seed-for-tests"

// TestDerivedKeyIsStable is the regression test for the defect this tool was rewritten to
// fix: the key must be a pure function of the seed and the role, because it is the only
// thing standing between a release and an identity nobody can upgrade across.
func TestDerivedKeyIsStable(t *testing.T) {
	first, err := deriveECKey(testSeed, "app")
	if err != nil {
		t.Fatalf("deriveECKey: %v", err)
	}
	second, err := deriveECKey(testSeed, "app")
	if err != nil {
		t.Fatalf("deriveECKey: %v", err)
	}
	if first.D.Cmp(second.D) != 0 {
		t.Fatalf("the same seed produced two different keys: %x then %x", first.D, second.D)
	}

	// Marshalling must be stable too, since that is what actually reaches apksigner.
	a, err := x509.MarshalECPrivateKey(first)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	b, err := x509.MarshalECPrivateKey(second)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("the same seed produced two different encoded private keys")
	}
}

// TestRolesAreIndependent checks the property the role label exists for: one seed yields
// cryptographically unrelated identities, so the APK key, the F-Droid index key and the
// APT key are not the same key wearing different hats.
func TestRolesAreIndependent(t *testing.T) {
	seen := map[string]string{}
	for _, role := range []string{"app", "fdroid", "debian"} {
		k, err := deriveECKey(testSeed, role)
		if err != nil {
			t.Fatalf("deriveECKey(%s): %v", role, err)
		}
		d := k.D.String()
		if other, clash := seen[d]; clash {
			t.Fatalf("roles %q and %q derived the same key", other, role)
		}
		seen[d] = role
	}
}

// TestScalarIsInRange guards the arithmetic. A P-256 private scalar must lie in [1, n-1];
// the previous implementation used the raw HKDF output, which is out of range with small
// but nonzero probability and is not what the curve parameters permit.
func TestScalarIsInRange(t *testing.T) {
	n := elliptic.P256().Params().N
	one := big.NewInt(1)

	for _, role := range []string{"app", "fdroid", "debian", "another-role"} {
		k, err := deriveECKey(testSeed, role)
		if err != nil {
			t.Fatalf("deriveECKey(%s): %v", role, err)
		}
		if k.D.Cmp(one) < 0 || k.D.Cmp(n) >= 0 {
			t.Fatalf("role %q: scalar out of range [1, n-1]", role)
		}
		// The public point must actually be on the curve and match the scalar.
		x, y := elliptic.P256().ScalarBaseMult(k.D.Bytes())
		if x.Cmp(k.X) != 0 || y.Cmp(k.Y) != 0 {
			t.Fatalf("role %q: public point does not match the private scalar", role)
		}
	}
}

// TestPassphraseIsStableAndPerRole covers the keystore passphrase, which replaced the
// literal string "syncthing" hardcoded in two workflows.
func TestPassphraseIsStableAndPerRole(t *testing.T) {
	app, err := derivePassphrase(testSeed, "app")
	if err != nil {
		t.Fatalf("derivePassphrase: %v", err)
	}
	again, err := derivePassphrase(testSeed, "app")
	if err != nil {
		t.Fatalf("derivePassphrase: %v", err)
	}
	if app != again {
		t.Fatalf("passphrase is not stable: %q then %q", app, again)
	}

	fdroid, err := derivePassphrase(testSeed, "fdroid")
	if err != nil {
		t.Fatalf("derivePassphrase: %v", err)
	}
	if app == fdroid {
		t.Fatal("every role got the same passphrase")
	}
	if len(app) < 32 {
		t.Fatalf("passphrase is too short to be worth deriving: %d chars", len(app))
	}
}

// TestDifferentSeedsDiverge is the check that makes the pinned-certificate comparison
// meaningful: a wrong MASTER_SECRET must not silently produce a usable-looking identity.
func TestDifferentSeedsDiverge(t *testing.T) {
	a, err := deriveECKey(testSeed, "app")
	if err != nil {
		t.Fatalf("deriveECKey: %v", err)
	}
	b, err := deriveECKey(testSeed+"x", "app")
	if err != nil {
		t.Fatalf("deriveECKey: %v", err)
	}
	if a.D.Cmp(b.D) == 0 {
		t.Fatal("two different seeds derived the same key")
	}
}

// TestOpenPGPKeyIsReproducible is the property that lets the APT signing key be derived
// rather than stored: unlike the ECDSA roles, an Ed25519 OpenPGP key with a pinned
// creation time reproduces byte for byte, self-signature included.
func TestOpenPGPKeyIsReproducible(t *testing.T) {
	a, err := deriveEd25519Key(testSeed, "debian")
	if err != nil {
		t.Fatalf("deriveEd25519Key: %v", err)
	}
	b, err := deriveEd25519Key(testSeed, "debian")
	if err != nil {
		t.Fatalf("deriveEd25519Key: %v", err)
	}
	if string(transferableSecretKey(a)) != string(transferableSecretKey(b)) {
		t.Fatal("the same seed produced two different transferable secret keys")
	}
	if string(transferablePublicKey(a)) != string(transferablePublicKey(b)) {
		t.Fatal("the same seed produced two different public keys")
	}
}

// TestOpenPGPFingerprintIsPinned locks the derivation contract. If this value changes,
// every apt client that trusts the old key stops accepting the repository, so it must only
// ever change deliberately.
func TestOpenPGPFingerprintIsPinned(t *testing.T) {
	priv, err := deriveEd25519Key(testSeed, "debian")
	if err != nil {
		t.Fatalf("deriveEd25519Key: %v", err)
	}
	const want = "8F7EA710B62792B3DA32DFE30A489B7D4CD0FDB3"
	got := fmt.Sprintf("%X", fingerprint(priv.Public().(ed25519.PublicKey)))
	if want != got {
		t.Fatalf("fingerprint for the test seed changed: got %s, want %s\n"+
			"Changing the derivation, the creation time or the user ID changes the key "+
			"identity and invalidates every client that trusts it.", got, want)
	}
}

// TestMPIEncoding covers the one hand-rolled encoding that a wrong result would make gpg
// reject with an unhelpful message.
func TestMPIEncoding(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want []byte
	}{
		{[]byte{}, []byte{0, 0}},
		{[]byte{0x00, 0x00}, []byte{0, 0}},
		{[]byte{0x01}, []byte{0, 1, 0x01}},
		{[]byte{0xFF}, []byte{0, 8, 0xFF}},
		{[]byte{0x00, 0xFF}, []byte{0, 8, 0xFF}},
		{[]byte{0x40, 0x00}, []byte{0, 15, 0x40, 0x00}},
	} {
		got := mpi(tc.in)
		if string(got) != string(tc.want) {
			t.Errorf("mpi(% x) = % x, want % x", tc.in, got, tc.want)
		}
	}
}
