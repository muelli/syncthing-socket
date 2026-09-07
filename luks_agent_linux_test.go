//go:build linux

package socket

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The reply framing is the one part of this that cannot be checked by reading the code:
// get it wrong and systemd simply ignores the datagram, which at boot looks identical to
// the network being down. So send a real one over a real socket and read it back.
func TestReplyToAsk(t *testing.T) {
	// A unix socket path has a hard length limit well under PATH_MAX, and the default
	// TempDir under some CI runners is long enough to hit it.
	dir, err := os.MkdirTemp("", "sck")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "sck.test")
	conn, err := net.ListenPacket("unixgram", socketPath)
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer conn.Close()

	const passphrase = "correct horse battery staple"
	if err := replyToAsk(socketPath, passphrase); err != nil {
		t.Fatalf("replyToAsk: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("nothing arrived on the socket: %v", err)
	}

	// systemd's protocol: "+" then the password, with no trailing newline and no NUL
	// terminator for a single password.
	if want := "+" + passphrase; string(buf[:n]) != want {
		t.Errorf("received %q, want %q", buf[:n], want)
	}
}

// Sending to a socket that is not there must be an error rather than a silent success,
// or the agent would mark a request answered when nothing received it.
func TestReplyToAskFailsWithoutAListener(t *testing.T) {
	dir, err := os.MkdirTemp("", "sck")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	if err := replyToAsk(filepath.Join(dir, "absent"), "x"); err == nil {
		t.Fatal("sending to a nonexistent socket reported success")
	}
}

// NotAfter is CLOCK_MONOTONIC, not wall clock. An initrd's idea of the date is regularly
// nonsense before the RTC is read, so comparing against time.Now would expire requests at
// random.
func TestMonotonicUsecAdvances(t *testing.T) {
	first, err := monotonicUsec()
	if err != nil {
		t.Fatalf("monotonicUsec: %v", err)
	}
	if first == 0 {
		t.Fatal("the monotonic clock read as zero")
	}
	time.Sleep(2 * time.Millisecond)
	second, err := monotonicUsec()
	if err != nil {
		t.Fatalf("monotonicUsec: %v", err)
	}
	if second <= first {
		t.Errorf("the clock did not advance: %d then %d", first, second)
	}
}
