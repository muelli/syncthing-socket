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

func TestCommandExecutionP2P(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-command-binary", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	passphrase := fmt.Sprintf("test-command-passphrase-%d", time.Now().UnixNano())

	// Start server in command mode
	// We'll execute a command that reads stdin and echoes it back, plus writes to stderr
	cmdServer := exec.Command("./test-command-binary", "server", "--passphrase", passphrase, "--command", "echo 'starting command'; cat -; echo 'error log' >&2", "--log-level", "debug", "--log-format", "text")
	var serverOutput bytes.Buffer
	cmdServer.Stdout = io.MultiWriter(os.Stdout, &serverOutput)
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Parse server output to find the relay URI to bypass global discovery rate limits
	relayURI := waitForRelayURI(t, &serverOutput, 30*time.Second)

	// Start client, explicitly passing the relay URI
	cmdClient := exec.Command("./test-command-binary", "client", "--passphrase", passphrase, "--relay", relayURI, "--log-level", "debug", "--log-format", "text")
	
	// Inject mock data to client's stdin, leaving it open to prevent premature client exit
	stdinRead, stdinWrite := io.Pipe()
	cmdClient.Stdin = stdinRead
	
	go func() {
		// Write immediately; it will be buffered and sent once the P2P connection establishes
		stdinWrite.Write([]byte("hello from client\n"))
	}()
	
	// Capture client output
	stdout, _ := cmdClient.StdoutPipe()
	var outputBuffer bytes.Buffer
	go io.Copy(&outputBuffer, stdout)
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	// Deterministic polling: wait until we see the expected output, up to 30 seconds
	deadline := time.Now().Add(30 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		output := outputBuffer.String()
		if strings.Contains(output, "starting command") && strings.Contains(output, "hello from client") {
			success = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Test timed out waiting for expected output. Got: %s", outputBuffer.String())
	}

	output := outputBuffer.String()
	// Note: stderr is not piped to the client by default unless 2>&1 is used, so it shouldn't be in the output.
	if strings.Contains(output, "error log") {
		t.Fatalf("Did not expect stderr 'error log' to be piped to client stdout")
	}

	t.Logf("Successfully executed remote command over P2P tunnel!")
}
