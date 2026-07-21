package main

import (
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
	cmdServer := exec.Command("./test-shell-binary", "server", "--passphrase", passphrase, "--shell", "--direct-port", "22002", "--discovery", "", "--relay", "", "--log-level", "debug", "--log-format", "text")
	cmdServer.Stdout = os.Stdout
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Wait briefly for local socket to bind
	time.Sleep(1 * time.Second)

	// Start client in shell mode, explicitly passing the direct TCP URI
	cmdClient := exec.Command("./test-shell-binary", "client", "--passphrase", passphrase, "--shell", "--relay", "tcp://127.0.0.1:22002", "--discovery", "", "--log-level", "debug", "--log-format", "text")
	
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
