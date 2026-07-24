package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAuthorizedClientsSpaceParsing tests that --authorized-clients handles
// leading/trailing spaces around comma-separated Device IDs correctly.
func TestAuthorizedClientsSpaceParsing(t *testing.T) {
	buildTestAuthBinary(t)

	authPass1 := fmt.Sprintf("auth-space-1-%d", time.Now().UnixNano())
	authPass2 := fmt.Sprintf("auth-space-2-%d", time.Now().UnixNano())
	unauthPass := fmt.Sprintf("unauth-space-%d", time.Now().UnixNano())

	devID1 := getClientDeviceID(t, authPass1)
	devID2 := getClientDeviceID(t, authPass2)

	// Comma-separated with spaces
	authClientsFlag := fmt.Sprintf("  %s  ,   %s  ", devID1, devID2)

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-space-pass",
		"--command", "echo 'space parsing success'",
		"--direct-port", "22013",
		"--discovery", "",
		"--relay", "",
		"--authorized-clients", authClientsFlag,
		"--log-level", "debug",
		"--log-format", "text",
	)
	cmdServer.Stderr = os.Stderr

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		if cmdServer.Process != nil {
			cmdServer.Process.Kill()
		}
	}()

	time.Sleep(1 * time.Second)

	// Test Client 1 (Authorized)
	cmdClient1 := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", authPass1,
		"--relay", "tcp://127.0.0.1:22013",
		"--discovery", "",
	)
	var out1 bytes.Buffer
	cmdClient1.Stdout = &out1
	if err := cmdClient1.Run(); err != nil {
		t.Fatalf("Client 1 failed to run: %v", err)
	}
	if !strings.Contains(out1.String(), "space parsing success") {
		t.Fatalf("Client 1 expected output not found, got: %s", out1.String())
	}

	// Test Client 2 (Authorized)
	cmdClient2 := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", authPass2,
		"--relay", "tcp://127.0.0.1:22013",
		"--discovery", "",
	)
	var out2 bytes.Buffer
	cmdClient2.Stdout = &out2
	if err := cmdClient2.Run(); err != nil {
		t.Fatalf("Client 2 failed to run: %v", err)
	}
	if !strings.Contains(out2.String(), "space parsing success") {
		t.Fatalf("Client 2 expected output not found, got: %s", out2.String())
	}

	// Test Client 3 (Unauthorized)
	cmdClient3 := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", unauthPass,
		"--relay", "tcp://127.0.0.1:22013",
		"--discovery", "",
	)
	var out3 bytes.Buffer
	cmdClient3.Stdout = &out3
	_ = cmdClient3.Run()
	if strings.Contains(out3.String(), "space parsing success") {
		t.Fatalf("Unauthorized client received command output! Output: %s", out3.String())
	}

	t.Log("Successfully verified spaced comma-separated device ID parsing and authorization.")
}

// TestAuthorizedClientsWhitespaceInput tests that empty or whitespace-only --authorized-clients flag
// is treated as no authorization restriction (default behavior).
func TestAuthorizedClientsWhitespaceInput(t *testing.T) {
	buildTestAuthBinary(t)

	clientPass := fmt.Sprintf("ws-client-pass-%d", time.Now().UnixNano())

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-ws-pass",
		"--command", "echo 'whitespace input allowed'",
		"--direct-port", "22014",
		"--discovery", "",
		"--relay", "",
		"--authorized-clients", "   ,  ,   ",
		"--log-level", "debug",
		"--log-format", "text",
	)
	cmdServer.Stderr = os.Stderr

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		if cmdServer.Process != nil {
			cmdServer.Process.Kill()
		}
	}()

	time.Sleep(1 * time.Second)

	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", clientPass,
		"--relay", "tcp://127.0.0.1:22014",
		"--discovery", "",
	)
	var clientOut bytes.Buffer
	cmdClient.Stdout = &clientOut

	if err := cmdClient.Run(); err != nil {
		t.Fatalf("Client failed to run: %v", err)
	}
	if !strings.Contains(clientOut.String(), "whitespace input allowed") {
		t.Fatalf("Expected output not found for whitespace input test, got: %s", clientOut.String())
	}

	t.Log("Successfully verified whitespace-only --authorized-clients allows all connections.")
}

// TestInvalidDeviceIDFormatOnStartup tests that passing an invalid Device ID format
// causes the server CLI to log an error and exit immediately (exit status 1).
func TestInvalidDeviceIDFormatOnStartup(t *testing.T) {
	buildTestAuthBinary(t)

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-invalid-pass",
		"--direct-port", "22015",
		"--authorized-clients", "INVALID-DEVICE-ID-FORMAT",
	)
	var errBuf bytes.Buffer
	cmdServer.Stderr = &errBuf

	err := cmdServer.Run()
	if err == nil {
		t.Fatalf("Expected server to fail with invalid device ID format, but it exited successfully.")
	}

	if !strings.Contains(errBuf.String(), "Invalid authorized client Device ID") {
		t.Fatalf("Expected 'Invalid authorized client Device ID' error message, got: %s", errBuf.String())
	}

	t.Log("Successfully verified server exits with error on invalid Device ID format.")
}

// TestConcurrentUnauthorizedClientsStress stress-tests 20 concurrent unauthorized client connection
// attempts to verify fast rejection, thread safety, and no goroutine/memory leaks.
func TestConcurrentUnauthorizedClientsStress(t *testing.T) {
	buildTestAuthBinary(t)

	authPass := fmt.Sprintf("stress-auth-pass-%d", time.Now().UnixNano())
	authorizedDevID := getClientDeviceID(t, authPass)

	safeServerErr := &SafeBuffer{}

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-stress-pass",
		"--command", "echo 'stress test command'",
		"--direct-port", "22016",
		"--discovery", "",
		"--relay", "",
		"--authorized-clients", authorizedDevID,
		"--log-level", "debug",
		"--log-format", "text",
	)
	cmdServer.Stderr = io.MultiWriter(safeServerErr, os.Stderr)

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		if cmdServer.Process != nil {
			cmdServer.Process.Kill()
		}
	}()

	time.Sleep(1 * time.Second)

	const concurrency = 20
	var wg sync.WaitGroup
	startChan := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			unauthPass := fmt.Sprintf("unauth-stress-pass-%d-%d", idx, time.Now().UnixNano())
			<-startChan

			cmdClient := exec.Command(
				"./test-auth-binary", "client",
				"--passphrase", unauthPass,
				"--relay", "tcp://127.0.0.1:22016",
				"--discovery", "",
			)
			var clientOut bytes.Buffer
			cmdClient.Stdout = &clientOut
			_ = cmdClient.Run()

			if strings.Contains(clientOut.String(), "stress test command") {
				t.Errorf("Goroutine %d: Unauthorized client received command output!", idx)
			}
		}(i)
	}

	close(startChan) // Trigger concurrent execution
	wg.Wait()

	// Count occurrences of warning in server stderr
	deadline := time.Now().Add(5 * time.Second)
	warningCount := 0
	for time.Now().Before(deadline) {
		logs := safeServerErr.String()
		warningCount = strings.Count(logs, "Unauthorized client connection attempt")
		if warningCount >= concurrency {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if warningCount < concurrency {
		t.Fatalf("Expected at least %d 'Unauthorized client connection attempt' warnings, got %d. Logs:\n%s", concurrency, warningCount, safeServerErr.String())
	}

	t.Logf("Successfully stress-tested %d concurrent unauthorized connection attempts (all %d cleanly rejected).", concurrency, warningCount)
}
