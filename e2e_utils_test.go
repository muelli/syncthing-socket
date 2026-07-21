package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// waitForRelayURI polls the server output buffer until it finds the "Joined relay" log line,
// extracts the URI, and returns it. It fails the test if it times out.
func waitForRelayURI(t *testing.T, serverOutput *bytes.Buffer, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		lines := strings.Split(serverOutput.String(), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Joined relay") {
				parts := strings.Split(line, "Joined relay ")
				if len(parts) == 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("Server failed to join relay within timeout. Output: %s", serverOutput.String())
	return ""
}
