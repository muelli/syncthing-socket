package socket

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelay stands in for syncthing's relay client, which re-picks a relay from the
// dynamic pool on every reconnect.
type fakeRelay struct{ uri *url.URL }

func (f *fakeRelay) URI() *url.URL { return f.uri }

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return u
}

// TestAnnounceAddressesFollowsTheRelay is the regression test for a server that quietly
// became unreachable. runServer used to read relayClient.URI() once and announce that
// address for the lifetime of the process. With the default dynamic+https:// pool the
// relay client picks a different relay whenever it reconnects, so discovery went on
// advertising a relay the server had left, and clients failed against every address they
// were handed.
//
// Observed in the field: the server was rejected from its first relay with "already
// connected", joined another, and discovery still listed only the previous addresses
// minutes later.
func TestAnnounceAddressesFollowsTheRelay(t *testing.T) {
	relay := &fakeRelay{uri: mustURL(t, "relay://198.51.100.1:22067/?id=AAA")}

	got := announceAddresses(relay, 0, 0)
	if len(got) != 1 || got[0] != "relay://198.51.100.1:22067/?id=AAA" {
		t.Fatalf("expected the current relay, got %v", got)
	}

	// The relay client reconnects elsewhere.
	relay.uri = mustURL(t, "relay://203.0.113.9:22067/?id=BBB")
	got = announceAddresses(relay, 0, 0)
	if len(got) != 1 || got[0] != "relay://203.0.113.9:22067/?id=BBB" {
		t.Fatalf("the address did not follow the relay change, got %v", got)
	}
}

// TestAnnounceAddressesHandlesNoRelay covers the direct-port-only and not-yet-connected
// cases; announcing an empty or half-formed address list is worse than announcing nothing.
func TestAnnounceAddressesHandlesNoRelay(t *testing.T) {
	if got := announceAddresses(nil, 0, 0); len(got) != 0 {
		t.Fatalf("expected no addresses without a relay or direct port, got %v", got)
	}
	if got := announceAddresses(nil, 22000, 34567); len(got) != 1 || got[0] != "tcp://:34567" {
		t.Fatalf("expected the bound direct port, got %v", got)
	}
	// Connected relay plus a direct port: both are reachable.
	relay := &fakeRelay{uri: mustURL(t, "relay://198.51.100.1:22067/?id=AAA")}
	if got := announceAddresses(relay, 22000, 34567); len(got) != 2 {
		t.Fatalf("expected both the relay and the direct port, got %v", got)
	}
	// A relay client that has not connected yet must not contribute an address.
	if got := announceAddresses(&fakeRelay{}, 0, 0); len(got) != 0 {
		t.Fatalf("expected nothing before the relay connects, got %v", got)
	}
}

// The invitations channel the relay client hands out is created with make() and is never
// closed by anything upstream, so a loop that waits only on it, treating a closed channel
// as "the relay is gone", waits forever once Serve gives up. This pins that property: if a
// future upstream ever does close it, the "ok" check becomes reachable and this test is
// the place to notice.
func TestRelayInvitationsChannelIsNeverClosedUpstream(t *testing.T) {
	root := "vendor/github.com/syncthing/syncthing/lib/relay/client"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("vendored relay client not present: %v", err)
	}

	found := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(body), "invitations") {
			found = true
		}
		if strings.Contains(string(body), "close(c.invitations)") ||
			strings.Contains(string(body), "close(invitations)") {
			t.Fatalf("%s now closes the invitations channel. The server's "+
				"invitation loops assume it never does and rely on watching "+
				"Serve's return instead; revisit that now it is reachable.",
				entry.Name())
		}
	}
	if !found {
		t.Fatal("no invitations channel found in the vendored relay client, so this " +
			"test is no longer checking what it claims to")
	}
}

// The server must notice when the relay client gives up. Without it the process keeps
// looking like a listening server while being unreachable, and its discovery record goes
// stale, which for a machine at its LUKS prompt means nobody can unlock it.
func TestServerWatchesForTheRelayClientStopping(t *testing.T) {
	body, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	src := string(body)

	if !strings.Contains(src, "relayStopped <- err") {
		t.Error("Serve's return value is not published anywhere, so nothing can react to it")
	}
	// Both invitation loops, the forwarding one and the netcat one, need to watch it.
	if n := strings.Count(src, "case err := <-relayStopped:"); n != 2 {
		t.Errorf("%d invitation loops watch for the relay stopping, want 2", n)
	}
}
