package socket

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestServerReceivesPayloadFromClient covers the phone-as-client topology: the machine being
// unlocked runs the *server* and waits, and the key holder (an Android app, which cannot
// keep a listening service alive in the background) connects and pushes the passphrase.
//
// For that to be usable from an initramfs the server must print what it receives and then
// exit, so a shell can capture it with $(...). Its own stdin is /dev/null, since there is
// nothing to send back.
func TestServerReceivesPayloadFromClient(t *testing.T) {
	const binary = "./test-reverse-payload-binary"
	if out, err := exec.Command("go", "build", "-o", binary, "./cmd/syncthing-socket").CombinedOutput(); err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, out)
	}
	defer os.Remove(binary)

	passphrase := fmt.Sprintf("test-reverse-payload-%d", time.Now().UnixNano())
	payload := "unlock-me-please"

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	server := exec.Command(binary, "server", "--passphrase", passphrase,
		"--direct-port", "22011", "--discovery", "", "--relay", "",
		"--log-level", "error", "--log-format", "text")
	server.Stdin = devNull // the booting machine has nothing to send
	var stdout bytes.Buffer
	server.Stdout = &stdout
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Process.Kill()

	time.Sleep(1 * time.Second)

	client := exec.Command(binary, "client", "--passphrase", passphrase,
		"--relay", "tcp://127.0.0.1:22011", "--discovery", "",
		"--log-level", "error", "--log-format", "text")
	client.Stdin = bytes.NewBufferString(payload)
	client.Stderr = os.Stderr
	if err := client.Run(); err != nil {
		t.Fatalf("Client failed: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- server.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Server exited with error: %v (stdout %q)", err, stdout.String())
		}
	case <-time.After(30 * time.Second):
		server.Process.Kill()
		t.Fatalf("Server did not exit after the client finished sending; a keyscript "+
			"capturing its output with $(...) would hang forever. stdout so far: %q",
			stdout.String())
	}

	if got := stdout.String(); got != payload {
		t.Fatalf("Payload lost or corrupted.\n got: %q\nwant: %q", got, payload)
	}
}

// TestServerStdoutCarriesOnlyThePayload guards the descriptor reservation in
// reservePipeStdout. In plain pipe mode a shell captures our stdout with $(...) and hands
// it to cryptsetup as a passphrase, so a single stray line makes the key wrong.
//
// Two different things have already leaked into it: our own connection banner, and
// syncthing's logger package, which hardcodes os.Stdout at package-init time and printed
// "Joined relay ..." straight into the key. That second one cost a boot: the passphrase
// arrived as 77 bytes instead of 10 and cryptsetup reported "bad password or options?".
//
// Trace logging would not have caught either: the bytes never crossed the connection.
// Asserting on the exact contents of stdout is what does.
func TestServerStdoutCarriesOnlyThePayload(t *testing.T) {
	const binary = "./test-stdout-purity-binary"
	if out, err := exec.Command("go", "build", "-o", binary, "./cmd/syncthing-socket").CombinedOutput(); err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, out)
	}
	defer os.Remove(binary)

	passphrase := fmt.Sprintf("test-stdout-purity-%d", time.Now().UnixNano())
	payload := "s3cret"

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	// --log-level info deliberately: the banner and the relay/discovery chatter must still
	// be produced, just never on stdout.
	server := exec.Command(binary, "server", "--passphrase", passphrase,
		"--direct-port", "22012", "--discovery", "", "--relay", "",
		"--log-level", "info", "--log-format", "text")
	server.Stdin = devNull
	var stdout, stderr bytes.Buffer
	server.Stdout = &stdout
	server.Stderr = &stderr
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Process.Kill()

	time.Sleep(1 * time.Second)

	client := exec.Command(binary, "client", "--passphrase", passphrase,
		"--relay", "tcp://127.0.0.1:22012", "--discovery", "",
		"--log-level", "error", "--log-format", "text")
	client.Stdin = bytes.NewBufferString(payload)
	if err := client.Run(); err != nil {
		t.Fatalf("Client failed: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		server.Process.Kill()
		t.Fatalf("Server did not exit; stdout so far: %q", stdout.String())
	}

	if got := stdout.String(); got != payload {
		t.Fatalf("stdout must contain the payload and nothing else.\n got: %q\nwant: %q",
			got, payload)
	}
	// The banner should still exist, just on the other stream.
	if !bytes.Contains(stderr.Bytes(), []byte("Server Device ID")) {
		t.Errorf("expected the informational banner on stderr, got: %q", stderr.String())
	}
}
