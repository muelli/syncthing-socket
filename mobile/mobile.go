package mobile

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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

// Server readiness, as reported to the unlock screen.
//
// These say what discovery knows, which is not the same as whether an unlock will work.
// They exist to answer "is it worth pressing the button yet?", and the honest answer is
// built entirely out of the record's timestamp: discovery keeps a record for more than an
// hour after a machine stops announcing, so presence alone would report a machine as
// waiting long after it had finished booting.
const (
	// StatusWaiting means the machine announced within the last couple of minutes. It is
	// almost certainly still sitting at its prompt.
	StatusWaiting = "WAITING"
	// StatusStale means there is a record, but an old one. The machine announced at some
	// point and may since have booted, been shut down, or lost its network. Trying costs
	// nothing, but do not promise the user it will work.
	StatusStale = "STALE"
	// StatusAbsent means discovery has nothing. Either the machine has not reached its
	// prompt yet, or it has been gone long enough for the record to expire. A freshly
	// booted machine takes around 30 seconds to appear.
	StatusAbsent = "ABSENT"
	// StatusNoNetwork means this phone could not reach discovery at all, so nothing is
	// known about the machine either way.
	StatusNoNetwork = "NO_NETWORK"
)

// freshRecordAge is how new a record must be to count as "waiting now".
//
// The machine announces every 60 seconds while it waits (see socket.UnlockAnnounceInterval),
// so this allows one missed announcement plus slack for the discovery server's own clock.
// Too tight and a waiting machine flickers to stale between announcements; too loose and
// this reports the machine as waiting after it has already booted.
const freshRecordAge = 150 * time.Second

// ServerStatus is the answer, shaped for gomobile: no time.Time, no slices.
type ServerStatus struct {
	State string
	// AgeSeconds is how long ago the machine last announced, or -1 when that is unknown
	// because there is no record or it carried no usable timestamp.
	AgeSeconds int
}

// CheckServerStatus asks discovery whether the machine looks like it is waiting.
//
// It is a single plain HTTPS GET on a public endpoint. It needs no seed and does no
// crypto, and deliberately does not connect to the machine: in the phone topology the
// machine serves exactly one connection and then stops waiting, so probing it would
// consume the very state being reported on and push the real unlock into a retry.
func CheckServerStatus(serverDeviceID string) (*ServerStatus, error) {
	return checkAgainst(socket.DefaultDiscoveryURL, serverDeviceID)
}

// checkAgainst is CheckServerStatus with the discovery endpoint injected, so the states
// can be tested against known responses rather than against whatever the public server
// happens to be saying today. Unexported: gomobile only needs the one entry point.
func checkAgainst(discoveryURL, serverDeviceID string) (*ServerStatus, error) {
	if strings.TrimSpace(serverDeviceID) == "" {
		return nil, fmt.Errorf("no device ID to look up")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	record, err := socket.LookupRecord(ctx, serverDeviceID, discoveryURL)
	if err != nil {
		// Cannot reach discovery, so nothing is known. This is not the same as the
		// machine being absent, and must not be shown as though it were.
		return &ServerStatus{State: StatusNoNetwork, AgeSeconds: -1}, nil
	}
	if record == nil {
		return &ServerStatus{State: StatusAbsent, AgeSeconds: -1}, nil
	}
	if record.Seen.IsZero() {
		// A record with no usable timestamp. It exists, so something announced, but its
		// age is exactly the thing we cannot vouch for.
		return &ServerStatus{State: StatusStale, AgeSeconds: -1}, nil
	}

	age := time.Since(record.Seen)
	if age < 0 {
		// Clock skew between this phone and the discovery server. Treat a record from
		// the future as new, since the alternative is calling a live machine stale.
		age = 0
	}
	state := StatusStale
	if age <= freshRecordAge {
		state = StatusWaiting
	}
	return &ServerStatus{State: state, AgeSeconds: int(age.Seconds())}, nil
}
