package mobile

import (
	"context"
	"fmt"
	"os"
	"strings"

	"syncthing-socket"
)

// UnlockLUKS connects to the target server and transmits the passphrase.
func UnlockLUKS(passphrase, p2pKeySeed, serverDeviceID string) error {
	// Temporarily override os.Stdin to pipe our passphrase into the client.
	// Since gomobile doesn't support passing io.Reader interfaces natively easily,
	// we use os.Pipe to simulate stdin for the underlying client logic.
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %v", err)
	}

	// Save original stdin and restore later
	originalStdin := os.Stdin
	defer func() { os.Stdin = originalStdin }()
	os.Stdin = r

	// Write passphrase and close the write end so the client knows it's EOF
	go func() {
		w.Write([]byte(passphrase))
		w.Close()
	}()

	// The suffix is not optional. Both ends derive their identity from the same seed and
	// must land on *different* certificates; without it this side's Device ID matches
	// neither the Server ID nor the Client ID that `syncthing-socket id --passphrase`
	// prints, so the peer rejects it. We connect, so we are the client.
	cert, err := socket.GenerateDeterministicCert(p2pKeySeed + socket.CertSuffixClient)
	if err != nil {
		return fmt.Errorf("failed to generate cert: %v", err)
	}

	// We run the client directly (in raw pipe mode, not as a shell/socks).
	// An empty discovery server yields a relative URL and fails every lookup, so pass the
	// same default the CLI uses. relayURIOverride stays empty: resolve via discovery.
	err = socket.RunClient(context.Background(), serverDeviceID, "", cert, socket.DefaultDiscoveryURL, true, "", false, "", "")

	if err != nil && !strings.Contains(err.Error(), "EOF") {
		return fmt.Errorf("client failed: %v", err)
	}

	return nil
}

// Unlock failure categories. The app maps these to advice a person can act on; the
// matching lives here, next to the errors it matches, rather than in Kotlin where it
// would drift silently the next time an error string changes.
const (
	// ErrServerOffline: the machine has not announced itself yet. Usually means it is
	// not at its unlock prompt, or its announcement has not reached global discovery,
	// which takes roughly 30 to 45 seconds after it comes up.
	ErrServerOffline = "SERVER_OFFLINE"
	// ErrServerUnreachable: discovery knows the machine but no advertised relay would
	// open a session. Typically a stale announcement from a previous boot.
	ErrServerUnreachable = "SERVER_UNREACHABLE"
	// ErrWrongDevice: the peer proved a different identity than the one enrolled. Either
	// the wrong machine, or someone impersonating it.
	ErrWrongDevice = "WRONG_DEVICE"
	// ErrNoNetwork: this phone could not reach discovery or the relay pool at all.
	ErrNoNetwork = "NO_NETWORK"
	// ErrRejected: the machine refused this phone's identity, so the enrolment on the
	// machine does not name this phone.
	ErrRejected = "REJECTED"
	ErrUnknown  = "UNKNOWN"
)

// ClassifyUnlockError turns an error from UnlockLUKS into one of the categories above.
// It takes the message rather than an error so gomobile can bind it.
func ClassifyUnlockError(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "not found in discovery"):
		return ErrServerOffline
	case strings.Contains(m, "invitation from relay"), strings.Contains(m, "join session"),
		strings.Contains(m, "not found"):
		return ErrServerUnreachable
	case strings.Contains(m, "security mismatch"):
		return ErrWrongDevice
	case strings.Contains(m, "discovery lookup failed"), strings.Contains(m, "no such host"),
		strings.Contains(m, "dial tcp"), strings.Contains(m, "network is unreachable"),
		strings.Contains(m, "timeout"), strings.Contains(m, "deadline exceeded"):
		return ErrNoNetwork
	case strings.Contains(m, "authentication failed"), strings.Contains(m, "unauthorized"):
		return ErrRejected
	default:
		return ErrUnknown
	}
}
