package socket

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pires/go-proxyproto"
)

func TestForwardProxyProtocolV2(t *testing.T) {
	// 1. Build binary
	cmdBuild := exec.Command("go", "build", "-o", "test-forward-binary", "./cmd/syncthing-socket")
	out, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, string(out))
		t.Fatalf("Failed to build binary: %v", err)
	}

	// 2. Start mock backend server that parses PROXY protocol
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer backendLn.Close()
	backendPort := backendLn.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := backendLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		header, err := proxyproto.Read(reader)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR: %v\n", err)))
			return
		}

		var deviceID string
		if tlvs, err := header.TLVs(); err == nil {
			for _, tlv := range tlvs {
				if tlv.Type == 0xEA {
					deviceID = string(tlv.Value)
				}
			}
		}

		conn.Write([]byte(fmt.Sprintf("PROXY_SRC:%s DEVICE_ID:%s\n", header.SourceAddr.String(), deviceID)))
		io.Copy(conn, reader)
	}()

	passphrase := fmt.Sprintf("test-forward-passphrase-%d", time.Now().UnixNano())
	directPort := "22004"

	// 3. Start syncthing-socket server
	cmdServer := exec.Command("./test-forward-binary", "server", "--passphrase", passphrase, "--forward", fmt.Sprintf("127.0.0.1:%d", backendPort), "--proxy-protocol", "--direct-port", directPort, "--discovery", "", "--relay", "", "--log-level", "debug", "--log-format", "text")
	cmdServer.Stdout = os.Stdout
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	time.Sleep(1 * time.Second)

	// 4. Start syncthing-socket client
	cmdClient := exec.Command("./test-forward-binary", "client", "--passphrase", passphrase, "--relay", "tcp://127.0.0.1:"+directPort, "--discovery", "", "--log-level", "debug", "--log-format", "text")

	stdinRead, stdinWrite := io.Pipe()
	cmdClient.Stdin = stdinRead

	go func() {
		// Send some data over the tunnel
		time.Sleep(1 * time.Second)
		stdinWrite.Write([]byte("hello backend\n"))
	}()

	stdout, _ := cmdClient.StdoutPipe()
	var outputBuffer bytes.Buffer
	go io.Copy(&outputBuffer, stdout)
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	// 5. Assert backend parsed header
	deadline := time.Now().Add(10 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		output := outputBuffer.String()
		if strings.Contains(output, "PROXY_SRC:") && strings.Contains(output, "DEVICE_ID:") && strings.Contains(output, "hello backend") {
			// Ensure DEVICE_ID is actually populated
			if !strings.Contains(output, "DEVICE_ID:\n") { 
				success = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Test timed out waiting for expected output. Got: %s", outputBuffer.String())
	}
}

func TestForwardUnixSocket(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-forward-binary", "./cmd/syncthing-socket")
	out, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, string(out))
		t.Fatalf("Failed to build binary: %v", err)
	}

	socketPath := fmt.Sprintf("/tmp/test_fwd_%d.sock", time.Now().UnixNano())
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)

	backendLn, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to listen on unix socket: %v", err)
	}
	defer backendLn.Close()

	go func() {
		conn, err := backendLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		header, err := proxyproto.Read(reader)
		if err != nil {
			conn.Write([]byte(fmt.Sprintf("ERROR: %v\n", err)))
			return
		}

		var deviceID string
		if tlvs, err := header.TLVs(); err == nil {
			for _, tlv := range tlvs {
				if tlv.Type == 0xEA {
					deviceID = string(tlv.Value)
				}
			}
		}

		protoStr := fmt.Sprintf("%v", header.TransportProtocol)
		if header.TransportProtocol == proxyproto.UnixStream {
			protoStr = "UnixStream"
		}
		conn.Write([]byte(fmt.Sprintf("PROXY_PROTO:%s DEVICE_ID:%s\n", protoStr, deviceID)))
		io.Copy(conn, reader)
	}()

	passphrase := fmt.Sprintf("test-forward-unix-passphrase-%d", time.Now().UnixNano())
	directPort := "22006"

	cmdServer := exec.Command("./test-forward-binary", "server", "--passphrase", passphrase, "--forward", "unix://"+socketPath, "--proxy-protocol", "--direct-port", directPort, "--discovery", "", "--relay", "", "--log-level", "debug", "--log-format", "text")
	cmdServer.Stdout = os.Stdout
	cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer cmdServer.Process.Kill()

	time.Sleep(1 * time.Second)

	cmdClient := exec.Command("./test-forward-binary", "client", "--passphrase", passphrase, "--relay", "tcp://127.0.0.1:"+directPort, "--discovery", "", "--log-level", "debug", "--log-format", "text")

	stdinRead, stdinWrite := io.Pipe()
	cmdClient.Stdin = stdinRead

	go func() {
		time.Sleep(1 * time.Second)
		stdinWrite.Write([]byte("hello unix backend\n"))
	}()

	stdout, _ := cmdClient.StdoutPipe()
	var outputBuffer bytes.Buffer
	go io.Copy(&outputBuffer, stdout)
	cmdClient.Stderr = os.Stderr

	if err := cmdClient.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer cmdClient.Process.Kill()

	deadline := time.Now().Add(10 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		output := outputBuffer.String()
		if strings.Contains(output, "PROXY_PROTO:UnixStream") && strings.Contains(output, "DEVICE_ID:") && strings.Contains(output, "hello unix backend") {
			if !strings.Contains(output, "DEVICE_ID:\n") {
				success = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Test timed out waiting for expected output. Got: %s", outputBuffer.String())
	}
}

