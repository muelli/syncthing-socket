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

func handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		fmt.Printf("MOCK_PROXY: Handling CONNECT to %s\n", r.Host)
		
		destConn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
			return
		}
		
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		
		// Send 200 OK Connection established
		clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		
		go io.Copy(destConn, clientConn)
		go io.Copy(clientConn, destConn)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func TestHTTPProxyRouting(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-proxy-binary", ".")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}

	proxyServer := &http.Server{
		Addr:    "127.0.0.1:8888",
		Handler: http.HandlerFunc(handleProxy),
	}
	go proxyServer.ListenAndServe()
	defer proxyServer.Close()

	passphrase := fmt.Sprintf("test-proxy-passphrase-%d", time.Now().UnixNano())

	cmdServer := exec.Command("./test-proxy-binary", "server", "-passphrase", passphrase, "-log-level", "debug", "-log-format", "text")
	cmdServer.Env = append(os.Environ(), "HTTP_PROXY=http://127.0.0.1:8888") // Inject proxy!
	
	// Capture output to look for our proxy logs
	outputChan := make(chan string, 100)
	stdout, _ := cmdServer.StdoutPipe()
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				outputChan <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	cmdServer.Stderr = os.Stderr

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	// Look for proxy logs or relay connection success
	timeout := time.After(15 * time.Second)
	var output string
	for {
		select {
		case msg := <-outputChan:
			output += msg
			if strings.Contains(output, "Connected to relay") || strings.Contains(output, "Joined relay") {
				t.Logf("Successfully established Relay connection via HTTP proxy!")
				return
			}
		case <-timeout:
			t.Fatalf("Timed out waiting for relay connection via proxy. Output: %s", output)
		}
	}
}
