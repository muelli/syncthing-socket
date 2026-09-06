package socket

import (
	"testing"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

// TestSeedSuffixesProduceDistinctIdentities pins down the derivation the whole pairing
// scheme rests on: the two ends of a connection must derive *different* certificates from
// the same seed, and those must be exactly what `syncthing-socket id --passphrase` prints.
//
// The gomobile bridge got this wrong by omitting the suffix entirely, which produced a
// third identity matching neither end; a silent, connection-refusing failure rather than
// a build error. Anything deriving an identity must go through the suffix constants.
func TestSeedSuffixesProduceDistinctIdentities(t *testing.T) {
	const seed = "a-shared-seed"

	id := func(suffix string) string {
		cert, err := GenerateDeterministicCert(seed + suffix)
		if err != nil {
			t.Fatalf("GenerateDeterministicCert(%q): %v", seed+suffix, err)
		}
		return syncthingprotocol.NewDeviceID(cert.Certificate[0]).String()
	}

	serverID := id(CertSuffixServer)
	clientID := id(CertSuffixClient)
	unsuffixed := id("")

	if serverID == clientID {
		t.Fatalf("server and client identities must differ, both are %s", serverID)
	}
	if unsuffixed == serverID || unsuffixed == clientID {
		t.Fatalf("an unsuffixed seed must not collide with either end (got %s)", unsuffixed)
	}

	// Deterministic across runs: the whole point is that both sides can recompute these
	// from the seed alone, on different machines and different releases.
	if got := id(CertSuffixServer); got != serverID {
		t.Fatalf("derivation is not deterministic: %s then %s", serverID, got)
	}
}
