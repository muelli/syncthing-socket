package socket

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --seed replaced --passphrase, which was never a passphrase: it is the seed both ends
// derive their transport identity from, and in the unlock case the other "passphrase" on
// the same command line is the one that unlocks the disk.
//
// The old spelling keeps working for a release so existing scripts do not break. That is a
// promise about behaviour, so it is tested by running the thing rather than by asserting
// something about the code: both spellings must yield byte-identical Device IDs, and only
// the deprecated one may warn.
func TestDeprecatedPassphraseFlagStillMatchesSeed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}

	binary := filepath.Join(t.TempDir(), "syncthing-socket")
	build := exec.Command("go", "build", "-o", binary, "./cmd/syncthing-socket")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	const seed = "a-seed-for-the-flag-equivalence-test"

	seedOut, err := exec.Command(binary, "id", "--seed", seed).Output()
	if err != nil {
		t.Fatalf("id --seed: %v", err)
	}
	passOut, err := exec.Command(binary, "id", "--passphrase", seed).Output()
	if err != nil {
		t.Fatalf("id --passphrase: %v", err)
	}
	if string(seedOut) != string(passOut) {
		t.Fatalf("the two spellings derive different identities:\n--seed:\n%s\n--passphrase:\n%s",
			seedOut, passOut)
	}
	if !strings.Contains(string(seedOut), "Server ID:") {
		t.Fatalf("no Server ID in the output, so this compared nothing:\n%s", seedOut)
	}

	// The deprecation has to be visible, or nobody migrates.
	var warn strings.Builder
	deprecated := exec.Command(binary, "id", "--passphrase", seed)
	deprecated.Stderr = &warn
	if err := deprecated.Run(); err != nil {
		t.Fatalf("id --passphrase: %v", err)
	}
	if !strings.Contains(warn.String(), "deprecated") {
		t.Errorf("--passphrase produced no deprecation warning on stderr, got %q", warn.String())
	}

	// And the new spelling must be quiet, or every unlock logs a warning to a console.
	var quiet strings.Builder
	current := exec.Command(binary, "id", "--seed", seed)
	current.Stderr = &quiet
	if err := current.Run(); err != nil {
		t.Fatalf("id --seed: %v", err)
	}
	if strings.Contains(quiet.String(), "deprecated") {
		t.Errorf("--seed warned about deprecation: %q", quiet.String())
	}
}
