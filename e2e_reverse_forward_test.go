package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestReverseForwardP2P(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-revfwd-binary", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	passphrase := fmt.Sprintf("test-revfwd-passphrase-%d", time.Now().UnixNano())

	// 1. Start a local mock HTTP server that the client will dial
	mockTargetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to bind mock target listener: %v", err)
	}
	defer mockTargetListener.Close()

	mockTargetPort := mockTargetListener.Addr().(*net.TCPAddr).Port
	mockTargetAddr := fmt.Sprintf("127.0.0.1:%d", mockTargetPort)

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Hello from the reverse forwarded client!"))
		})
		http.Serve(mockTargetListener, mux)
	}()

	// 2. Select a random port for the server to bind for reverse forwarding
	serverBindListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to bind test server port: %v", err)
	}
	serverBindPort := serverBindListener.Addr().(*net.TCPAddr).Port
	serverBindAddr := fmt.Sprintf("127.0.0.1:%d", serverBindPort)
	serverBindListener.Close() // Free it so the server can bind it

	// 3. Start Server with --reverse-forward
	cmdServer := exec.Command("./test-revfwd-binary", "server", "--passphrase", passphrase, "--reverse-forward", serverBindAddr, "--direct-port", "22005", "--discovery", "", "--relay", "", "--log-level", "debug", "--log-format", "text")
	cmdServer.Stdout = os.Stdout
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Wait briefly for local socket to bind
	time.Sleep(1 * time.Second)

	// 4. Start Client with --reverse-forward
	cmdClient := exec.Command("./test-revfwd-binary", "client", "--passphrase", passphrase, "--reverse-forward", mockTargetAddr, "--relay", "tcp://127.0.0.1:22005", "--discovery", "", "--log-level", "debug", "--log-format", "text")
	cmdClient.Stdout = os.Stdout
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	// Wait for P2P connection and yamux initialization
	time.Sleep(3 * time.Second)

	// 5. Test the reverse tunnel by sending an HTTP request to the server's bind address
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + serverBindAddr + "/")
	if err != nil {
		t.Fatalf("Failed to perform HTTP request against reverse forward listener: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	expected := "Hello from the reverse forwarded client!"
	if !strings.Contains(string(body), expected) {
		t.Fatalf("Expected response to contain %q, got %q", expected, string(body))
	}

	t.Log("Successfully accessed client-side HTTP server via reverse P2P tunnel!")
}
