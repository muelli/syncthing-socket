package socket

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
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
	cert, err := GenerateDeterministicCert(clientPassphrase + "client")
	if err != nil {
		t.Fatalf("Failed to generate cert for client passphrase %q: %v", clientPassphrase, err)
	}
	return syncthingprotocol.NewDeviceID(cert.Certificate[0]).String()
}

func getServerDeviceID(t *testing.T, serverPassphrase string) string {
	cert, err := GenerateDeterministicCert(serverPassphrase + "server")
	if err != nil {
		t.Fatalf("Failed to generate cert for server passphrase %q: %v", serverPassphrase, err)
	}
	return syncthingprotocol.NewDeviceID(cert.Certificate[0]).String()
}


func buildTestAuthBinary(t *testing.T) {
	cmdBuild := exec.Command("go", "build", "-o", "test-auth-binary", "./cmd/syncthing-socket")
	out, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, string(out))
		t.Fatalf("Failed to build test-auth-binary: %v", err)
	}
}

// TestUnauthorizedClientDropped tests that an unauthorized client's connection
// is closed by the server when --authorized-clients is enabled.
func TestUnauthorizedClientDropped(t *testing.T) {
	buildTestAuthBinary(t)

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-1",
		"--authorized-clients", "7SGFZYR-DXPRRF5-6QVKNME-XNUMTDU-XJ5KSHQ-HVCYWWU-XJDBIYB-TKWLDAJ", // Random dummy ID
		"--command", "echo 'should not print'",
		"--direct-port", "22010",
		"--discovery", "",
		"--relay", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	serverOut := &SafeBuffer{}
	cmdServer.Stdout = serverOut
	cmdServer.Stderr = serverOut

	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		if cmdServer.Process != nil {
			cmdServer.Process.Kill()
		}
	}()

	time.Sleep(1 * time.Second)
	serverDevID := getServerDeviceID(t, "server-auth-pass-1")

	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", "client-unauthorized-pass",
		"--relay", "tcp://127.0.0.1:22010",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
		serverDevID,
	)

	clientOut := &SafeBuffer{}
	cmdClient.Stdout = clientOut
	cmdClient.Stderr = clientOut

	if err := cmdClient.Run(); err == nil {
		t.Fatalf("Expected unauthorized client to fail, but it exited 0")
	}

	deadline := time.Now().Add(10 * time.Second)
	dropped := false
	for time.Now().Before(deadline) {
		if strings.Contains(serverOut.String(), "Unauthorized client connection attempt") {
			dropped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !dropped {
		t.Fatalf("Server logs did not record dropping unauthorized client.\nLogs:\n%s", serverOut.String())
	}

	t.Log("Successfully verified unauthorized client connection was dropped and warning logged.")
}

// TestAuthorizedClientSucceeds tests that a client whose ID is explicitly listed
// in --authorized-clients can connect and execute a command.
func TestAuthorizedClientSucceeds(t *testing.T) {
	buildTestAuthBinary(t)

	clientPass := "client-authorized-pass-2"
	clientDevID := getClientDeviceID(t, clientPass)

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-2",
		"--authorized-clients", clientDevID,
		"--command", "echo 'authorized client success' && sleep 0.5",
		"--direct-port", "22011",
		"--discovery", "",
		"--relay", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	serverOut := &SafeBuffer{}
	cmdServer.Stdout = serverOut
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
	serverDevID := getServerDeviceID(t, "server-auth-pass-2")

	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", clientPass,
		"--relay", "tcp://127.0.0.1:22011",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
		serverDevID,
	)

	clientOut := &SafeBuffer{}
	cmdClient.Stdout = clientOut
	cmdClient.Stderr = os.Stderr
	stdin, _ := cmdClient.StdinPipe()
	defer stdin.Close()

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

// TestClientWithoutAuthorizedClientsFlagSucceeds ensures backward compatibility:
// omitting --authorized-clients allows any client to connect.
func TestClientWithoutAuthorizedClientsFlagSucceeds(t *testing.T) {
	buildTestAuthBinary(t)

	clientPass := "client-any-pass-3"

	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-auth-pass-3",
		"--command", "echo 'default no auth flag success' && sleep 0.5",
		"--direct-port", "22012",
		"--discovery", "",
		"--relay", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	serverOut := &SafeBuffer{}
	cmdServer.Stdout = serverOut
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
	serverDevID := getServerDeviceID(t, "server-auth-pass-3")

	cmdClient := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", clientPass,
		"--relay", "tcp://127.0.0.1:22012",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
		serverDevID,
	)

	clientOut := &SafeBuffer{}
	cmdClient.Stdout = clientOut
	cmdClient.Stderr = os.Stderr
	stdin, _ := cmdClient.StdinPipe()
	defer stdin.Close()

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

// TestTOTPAuthenticationFailure tests that a client presenting an invalid TOTP passcode is rejected.
func TestTOTPAuthenticationFailure(t *testing.T) {
	buildTestAuthBinary(t)

	secret := "JBSWY3DPEHPK3PXP"
	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-totp-fail",
		"--totp-secret", secret,
		"--command", "echo 'should not execute'",
		"--direct-port", "22015",
		"--discovery", "",
		"--relay", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	serverOut := &SafeBuffer{}
	cmdServer.Stdout = serverOut
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
	serverDevID := getServerDeviceID(t, "server-totp-fail")

	cmdClientFail := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", "client-totp-fail",
		"--totp", "000000",
		"--relay", "tcp://127.0.0.1:22015",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
		serverDevID,
	)
	if err := cmdClientFail.Run(); err == nil {
		t.Fatalf("Expected client with invalid TOTP to fail, but it exited 0")
	}

	t.Log("Successfully verified invalid TOTP passcode is rejected.")
}

// TestTOTPAuthenticationSuccess tests that a client presenting a valid TOTP passcode succeeds.
func TestTOTPAuthenticationSuccess(t *testing.T) {
	buildTestAuthBinary(t)

	secret := "JBSWY3DPEHPK3PXP"
	cmdServer := exec.Command(
		"./test-auth-binary", "server",
		"--passphrase", "server-totp-success",
		"--totp-secret", secret,
		"--command", "echo 'totp success' && sleep 0.5",
		"--direct-port", "22016",
		"--discovery", "",
		"--relay", "",
		"--log-level", "debug",
		"--log-format", "text",
	)

	serverOut := &SafeBuffer{}
	cmdServer.Stdout = serverOut
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
	serverDevID := getServerDeviceID(t, "server-totp-success")

	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to generate TOTP code: %v", err)
	}

	cmdClientSuccess := exec.Command(
		"./test-auth-binary", "client",
		"--passphrase", "client-totp-success",
		"--totp", code,
		"--relay", "tcp://127.0.0.1:22016",
		"--discovery", "",
		"--log-level", "debug",
		"--log-format", "text",
		serverDevID,
	)

	clientOut := &SafeBuffer{}
	cmdClientSuccess.Stdout = clientOut
	cmdClientSuccess.Stderr = os.Stderr
	stdin, _ := cmdClientSuccess.StdinPipe()
	defer stdin.Close()

	if err := cmdClientSuccess.Start(); err != nil {
		t.Fatalf("Failed to start success client: %v", err)
	}
	defer func() {
		if cmdClientSuccess.Process != nil {
			cmdClientSuccess.Process.Kill()
		}
	}()

	deadline := time.Now().Add(30 * time.Second)
	success := false
	for time.Now().Before(deadline) {
		if strings.Contains(clientOut.String(), "totp success") {
			success = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Fatalf("Timed out waiting for TOTP client output. Got:\n%s", clientOut.String())
	}

	t.Log("Successfully verified valid TOTP passcode authenticates cleanly.")
}
