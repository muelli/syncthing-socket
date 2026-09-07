package socket

import (
	"net/url"
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
