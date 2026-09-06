package main

import (
	"crypto/elliptic"
	"crypto/x509"
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
