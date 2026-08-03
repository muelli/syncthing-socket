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

	cert, err := socket.GenerateDeterministicCert(p2pKeySeed)
	if err != nil {
		return fmt.Errorf("failed to generate cert: %v", err)
	}

	// We run the client directly (in raw pipe mode, not as a shell/socks).
	// discoveryServer and relayURIOverride are left empty to use global defaults.
	err = socket.RunClient(context.Background(), serverDeviceID, "", cert, "", true, "", false, "", "")
	
	if err != nil && !strings.Contains(err.Error(), "EOF") {
		return fmt.Errorf("client failed: %v", err)
	}
	
	return nil
}
