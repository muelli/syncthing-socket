package socket

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

// blackholeRelay accepts connections and then says nothing, which is how a relay that has
// gone away without closing its port behaves. Dialling one costs the full invitation
// timeout.
func blackholeRelay(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold it open and never reply.
			t.Cleanup(func() { conn.Close() })
		}
	}()
	return fmt.Sprintf("relay://%s", listener.Addr().String())
}

// The point of racing the advertised relays: global discovery accumulates every relay a
// device has ever announced from, so a record gathers dead entries with every boot. Tried
// in turn, each one that hangs rather than refusing costs the full invitation timeout, and
// the wait grows without bound. Raced, the whole set costs one timeout.
//
// Two blackholes are enough to tell the two apart: serially they would take about twice
// the invitation timeout, in parallel about one.
func TestDialFastestRelayRacesRatherThanQueues(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about as long as one relay invitation timeout")
	}

	addresses := []string{blackholeRelay(t), blackholeRelay(t), blackholeRelay(t)}

	cert, err := GenerateDeterministicCert("a-seed-for-the-relay-race-test")
	if err != nil {
		t.Fatalf("GenerateDeterministicCert: %v", err)
	}
	serverID := syncthingprotocol.NewDeviceID(cert.Certificate[0])

	start := time.Now()
	conn, err := dialFastestRelay(context.Background(), addresses, serverID, cert)
	elapsed := time.Since(start)

	if err == nil {
		conn.Close()
		t.Fatal("a set of blackhole relays somehow produced a session")
	}

	// The invitation timeout is 15s. Three in turn would be about 45; raced, about 15.
	// 25 is comfortably between the two and tolerant of a slow machine.
	if elapsed > 25*time.Second {
		t.Errorf("three unresponsive relays took %s, which is long enough that they "+
			"were tried one after another rather than raced", elapsed.Round(time.Second))
	}
	t.Logf("three unresponsive relays resolved in %s", elapsed.Round(time.Second))
}

// An unparsable address must not abort the race or be counted as an attempt.
func TestDialFastestRelaySkipsUnparsableAddresses(t *testing.T) {
	cert, err := GenerateDeterministicCert("another-seed")
	if err != nil {
		t.Fatalf("GenerateDeterministicCert: %v", err)
	}
	serverID := syncthingprotocol.NewDeviceID(cert.Certificate[0])

	_, err = dialFastestRelay(context.Background(),
		[]string{"://not a url", "relay://\x7f"}, serverID, cert)
	if err == nil {
		t.Fatal("garbage addresses produced a session")
	}
}

// A cancelled context must abandon the race rather than wait out the timeouts.
func TestDialFastestRelayHonoursContextCancellation(t *testing.T) {
	addresses := []string{blackholeRelay(t)}

	cert, err := GenerateDeterministicCert("a-third-seed")
	if err != nil {
		t.Fatalf("GenerateDeterministicCert: %v", err)
	}
	serverID := syncthingprotocol.NewDeviceID(cert.Certificate[0])

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := dialFastestRelay(ctx, addresses, serverID, cert); err == nil {
		t.Fatal("a cancelled race produced a session")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %s to take effect; the race is not watching ctx",
			elapsed.Round(time.Second))
	}
}
