package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

	runClientTest := func(clientPass string, shouldSucceed bool) {
		cmdServer := exec.Command(
			"./test-auth-binary", "server",
			"--passphrase", "server-space-pass",
			"--command", "echo 'space parsing success' && sleep 0.5",
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

		serverDevID := getServerDeviceID(t, "server-space-pass")

		cmdClient := exec.Command(
			"./test-auth-binary", "client",
			"--passphrase", clientPass,
			"--relay", "tcp://127.0.0.1:22013",
			"--discovery", "",
			serverDevID,
		)
		var out bytes.Buffer
		cmdClient.Stdout = &out
		in, _ := cmdClient.StdinPipe()
		defer in.Close()
		err := cmdClient.Run()

		if shouldSucceed {
			if err != nil {
				t.Fatalf("Client failed to run: %v", err)
			}
			if !strings.Contains(out.String(), "space parsing success") {
				t.Fatalf("Client expected output not found, got: %s", out.String())
			}
		} else {
			if strings.Contains(out.String(), "space parsing success") {
				t.Fatalf("Unauthorized client received command output! Output: %s", out.String())
			}
		}
	}

	// Test Client 1 (Authorized)
	runClientTest(authPass1, true)
	// Test Client 2 (Authorized)
	runClientTest(authPass2, true)
	// Test Client 3 (Unauthorized)
	runClientTest(unauthPass, false)

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
		"--command", "echo 'whitespace input allowed' && sleep 0.5",
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

	serverDevID := getServerDeviceID(t, "server-ws-pass")
	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", clientPass,
		"--relay", "tcp://127.0.0.1:22014",
		"--discovery", "",
		serverDevID,
	)
	var clientOut bytes.Buffer
	cmdClient.Stdout = &clientOut
	inC, _ := cmdClient.StdinPipe()
	defer inC.Close()

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


