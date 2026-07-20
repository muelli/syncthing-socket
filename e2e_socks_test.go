package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSocksProxyE2E(t *testing.T) {
	// 1. Build the binary
	cmdBuild := exec.Command("go", "build", "-o", "test-syncthing-socket", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	// Start local test HTTP server
	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello via SOCKS!")
	})
	go http.ListenAndServe("127.0.0.1:9090", nil)

	passphrase := fmt.Sprintf("test-socks-e2e-passphrase-%d", time.Now().UnixNano())
	
	// 2. Start server
	cmdServer := exec.Command("./test-syncthing-socket", "server", "-passphrase", passphrase, "-socks", "-log-level", "debug")
	cmdServer.Stdout = os.Stdout
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Wait for server to connect to relay and announce itself
	time.Sleep(15 * time.Second)

	// 3. Start client (notice we don't need the Server ID positional argument because of the passphrase!)
	cmdClient := exec.Command("./test-syncthing-socket", "client", "-passphrase", passphrase, "-socks", "127.0.0.1:10800", "-log-level", "debug")
	cmdClient.Stdout = os.Stdout
	cmdClient.Stderr = os.Stderr
	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	// Wait for client to connect, negotiate ICE/Relay, and start SOCKS proxy
	time.Sleep(10 * time.Second)

	// 4. Test SOCKS proxy
	proxyURL, _ := url.Parse("socks5://127.0.0.1:10800")
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get("http://127.0.0.1:9090/test")
	if err != nil {
		t.Fatalf("Failed to make request via SOCKS proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "Hello via SOCKS!") {
		t.Logf("Successfully routed traffic through SOCKS5 multiplexed P2P tunnel!")
	} else {
		t.Errorf("Unexpected response: %s", string(body))
	}
}
