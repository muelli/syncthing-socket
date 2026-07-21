package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestShellP2P(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-shell-binary", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	passphrase := fmt.Sprintf("test-shell-passphrase-%d", time.Now().UnixNano())
	testFile := fmt.Sprintf("/tmp/shell_test_out_%d.txt", time.Now().UnixNano())
	defer os.Remove(testFile)

	// Start server in shell mode
	cmdServer := exec.Command("./test-shell-binary", "server", "--passphrase", passphrase, "--shell", "--log-level", "debug", "--log-format", "text")
	var serverOutput bytes.Buffer
	cmdServer.Stdout = io.MultiWriter(os.Stdout, &serverOutput)
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Parse server output to find the relay URI to bypass global discovery rate limits
	deadlineRelay := time.Now().Add(30 * time.Second)
	relayURI := ""
	for time.Now().Before(deadlineRelay) {
		lines := strings.Split(serverOutput.String(), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Joined relay") {
				parts := strings.Split(line, "Joined relay ")
				if len(parts) == 2 {
					relayURI = strings.TrimSpace(parts[1])
				}
				break
			}
		}
		if relayURI != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if relayURI == "" {
		t.Fatalf("Server failed to join relay within timeout")
	}

	// Start client in shell mode, explicitly passing the relay URI
	cmdClient := exec.Command("./test-shell-binary", "client", "--passphrase", passphrase, "--relay", relayURI, "--shell", "--log-level", "debug", "--log-format", "text")
	
	// Inject mock command into client's stdin, using a pipe so it doesn't instantly EOF
	stdinRead, stdinWrite := io.Pipe()
	cmdClient.Stdin = stdinRead
	
	go func() {
		// Wait for the WebRTC connection to establish before firing the command
		// We'll write it repeatedly just in case
		for i := 0; i < 5; i++ {
			time.Sleep(3 * time.Second)
			stdinWrite.Write([]byte(fmt.Sprintf("echo 'hello from pty' > %s\n", testFile)))
		}
	}()
	
	// Stream output directly to test logs
	cmdClient.Stdout = os.Stdout
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	// Deterministic polling: wait for the client to create the output file
	deadline := time.Now().Add(30 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		out, err := os.ReadFile(testFile)
		if err == nil && strings.Contains(string(out), "hello from pty") {
			success = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Failed to read test output file or timed out waiting for shell execution.")
	}

	t.Logf("Successfully executed commands over multiplexed PTY shell session!")
}
