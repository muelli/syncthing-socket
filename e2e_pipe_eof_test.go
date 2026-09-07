package socket

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestPipePayloadSurvivesStdinEOF is the regression test for the LUKS unlock use case.
//
// The key holder runs `printf %s secret | syncthing-socket server ...` and the booting
// machine runs `syncthing-socket client ...` from a cryptsetup keyscript, where stdin is
// whatever early boot happens to hand it, frequently /dev/null, i.e. already at EOF.
//
// Both ends must therefore survive their *local* stdin reaching EOF: the payload has to
// arrive on the client's stdout, and the client has to exit once the server is done
// sending. Before the half-close fix, either side hitting EOF tore the whole connection
// down and the payload was silently lost, with the client still exiting 0.
func TestPipePayloadSurvivesStdinEOF(t *testing.T) {
	const binary = "./test-pipe-eof-binary"
	if out, err := exec.Command("go", "build", "-o", binary, "./cmd/syncthing-socket").CombinedOutput(); err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, out)
	}
	defer os.Remove(binary)

	passphrase := fmt.Sprintf("test-pipe-eof-%d", time.Now().UnixNano())
	payload := "correct horse battery staple"

	server := exec.Command(binary, "server", "--passphrase", passphrase,
		"--direct-port", "22010", "--discovery", "", "--relay", "",
		"--log-level", "error", "--log-format", "text")
	// Payload then immediate EOF, exactly like `printf %s secret | syncthing-socket server`.
	server.Stdin = bytes.NewBufferString(payload)
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Process.Kill()

	time.Sleep(1 * time.Second)

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	client := exec.Command(binary, "client", "--passphrase", passphrase,
		"--relay", "tcp://127.0.0.1:22010", "--discovery", "",
		"--log-level", "error", "--log-format", "text")
	client.Stdin = devNull // the keyscript case: stdin is already at EOF
	var stdout bytes.Buffer
	client.Stdout = &stdout
	client.Stderr = os.Stderr

	if err := client.Start(); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- client.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Client exited with error: %v (stdout %q)", err, stdout.String())
		}
	case <-time.After(30 * time.Second):
		client.Process.Kill()
		t.Fatalf("Client did not exit after the server finished sending; stdout so far: %q", stdout.String())
	}

	if got := stdout.String(); got != payload {
		t.Fatalf("Payload lost or corrupted over the wire.\n got: %q\nwant: %q", got, payload)
	}
}
