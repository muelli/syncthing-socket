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

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

// SafeBuffer is a thread-safe bytes.Buffer wrapper to avoid data races when
// reading logs while a process is writing to Stderr.
type SafeBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (s *SafeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *SafeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func getClientDeviceID(t *testing.T, clientPassphrase string) string {
	cert, err := generateDeterministicCert(clientPassphrase + "client")
	if err != nil {
		t.Fatalf("Failed to generate cert for client passphrase %q: %v", clientPassphrase, err)
	}
	return syncthingprotocol.NewDeviceID(cert.Certificate[0]).String()
}

func buildTestAuthBinary(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-auth-binary", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build test-auth-binary: %v", err)
	}
}

// TestUnauthorizedClientDropped tests that an unauthorized client's connection
// attempt is dropped post-handshake and an slog warning is logged on the server.
func TestUnauthorizedClientDropped(t *testing.T) {
	buildTestAuthBinary(t)

	authClientPass := fmt.Sprintf("auth-client-pass-%d", time.Now().UnixNano())
	unauthClientPass := fmt.Sprintf("unauth-client-pass-%d", time.Now().UnixNano())

	authorizedDevID := getClientDeviceID(t, authClientPass)

	safeServerErr := &SafeBuffer{}

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-1",
		"--command", "echo 'unauthorized connection should not execute command'",
		"--direct-port", "22010",
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

	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", unauthClientPass,
		"--relay", "tcp://127.0.0.1:22010",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	var clientOut bytes.Buffer
	cmdClient.Stdout = &clientOut
	cmdClient.Stderr = os.Stderr

	_ = cmdClient.Run()

	deadline := time.Now().Add(10 * time.Second)
	warningFound := false
	for time.Now().Before(deadline) {
		logs := safeServerErr.String()
		if strings.Contains(logs, "Unauthorized client connection attempt") {
			warningFound = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !warningFound {
		t.Fatalf("Expected 'Unauthorized client connection attempt' log in server output. Got:\n%s", safeServerErr.String())
	}

	if strings.Contains(clientOut.String(), "unauthorized connection should not execute command") {
		t.Fatalf("Unauthorized client received command output! Output: %s", clientOut.String())
	}

	t.Log("Successfully verified unauthorized client connection was dropped and warning logged.")
}

// TestAuthorizedClientSucceeds tests that a client listed in --authorized-clients connects successfully.
func TestAuthorizedClientSucceeds(t *testing.T) {
	buildTestAuthBinary(t)

	authClientPass := fmt.Sprintf("auth-client-pass-%d", time.Now().UnixNano())
	authorizedDevID := getClientDeviceID(t, authClientPass)

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-2",
		"--command", "echo 'authorized client success'",
		"--direct-port", "22011",
		"--discovery", "",
		"--relay", "",
		"--authorized-clients", authorizedDevID,
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
		"--passphrase", authClientPass,
		"--relay", "tcp://127.0.0.1:22011",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	var clientOut bytes.Buffer
	cmdClient.Stdout = &clientOut
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer func() {
		if cmdClient.Process != nil {
			cmdClient.Process.Kill()
		}
	}()

	deadline := time.Now().Add(30 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		if strings.Contains(clientOut.String(), "authorized client success") {
			success = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Timed out waiting for authorized client output. Got:\n%s", clientOut.String())
	}

	t.Log("Successfully verified authorized client connection succeeded.")
}

// TestClientWithoutAuthorizedClientsFlagSucceeds tests that when --authorized-clients is omitted, connections succeed by default.
func TestClientWithoutAuthorizedClientsFlagSucceeds(t *testing.T) {
	buildTestAuthBinary(t)

	clientPass := fmt.Sprintf("default-client-pass-%d", time.Now().UnixNano())

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-3",
		"--command", "echo 'default no auth flag success'",
		"--direct-port", "22012",
		"--discovery", "",
		"--relay", "",
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
		"--relay", "tcp://127.0.0.1:22012",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	var clientOut bytes.Buffer
	cmdClient.Stdout = &clientOut
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer func() {
		if cmdClient.Process != nil {
			cmdClient.Process.Kill()
		}
	}()

	deadline := time.Now().Add(30 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		if strings.Contains(clientOut.String(), "default no auth flag success") {
			success = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Timed out waiting for default client output. Got:\n%s", clientOut.String())
	}

	t.Log("Successfully verified default client connection without auth flag succeeded.")
}
