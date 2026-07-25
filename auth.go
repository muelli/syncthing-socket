package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/pquerna/otp/totp"
	"golang.org/x/term"
)

type AuthChallenge struct {
	Type    string   `json:"type"`              // "auth_required" or "auth_ok"
	Methods []string `json:"methods,omitempty"` // e.g. ["totp"]
}

type AuthResponse struct {
	Type string `json:"type"`           // "auth"
	TOTP string `json:"totp,omitempty"` // 6-digit code
}

type AuthResult struct {
	Status  string `json:"status"`            // "ok" or "error"
	Message string `json:"message,omitempty"` // Error details
}

func sendAuthMessage(conn net.Conn, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(b)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}
	_, err = conn.Write(b)
	return err
}

func readAuthMessage(conn net.Conn, v interface{}) error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return err
	}
	l := binary.BigEndian.Uint32(lenBuf)
	if l > 10*1024*1024 { // 10MB sanity check
		return fmt.Errorf("auth message too large: %d bytes", l)
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(conn, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// performServerAuth is called immediately after TLS handshake on the server side.
func performServerAuth(conn net.Conn, totpSecret string) error {
	if totpSecret == "" {
		return sendAuthMessage(conn, AuthChallenge{Type: "auth_ok"})
	}

	if err := sendAuthMessage(conn, AuthChallenge{Type: "auth_required", Methods: []string{"totp"}}); err != nil {
		return err
	}

	var resp AuthResponse
	if err := readAuthMessage(conn, &resp); err != nil {
		return err
	}

	if resp.Type != "auth" {
		_ = sendAuthMessage(conn, AuthResult{Status: "error", Message: fmt.Sprintf("expected auth message, got %s", resp.Type)})
		return fmt.Errorf("expected auth message, got %s", resp.Type)
	}

	if !totp.Validate(resp.TOTP, totpSecret) {
		_ = sendAuthMessage(conn, AuthResult{Status: "error", Message: "invalid TOTP passcode"})
		return fmt.Errorf("invalid TOTP passcode received")
	}

	return sendAuthMessage(conn, AuthResult{Status: "ok"})
}

// performClientAuth is called immediately after TLS handshake on the client side.
func performClientAuth(conn net.Conn, totpPasscode string) error {
	var challenge AuthChallenge
	if err := readAuthMessage(conn, &challenge); err != nil {
		return fmt.Errorf("failed to read auth challenge from server: %w", err)
	}

	if challenge.Type == "auth_ok" {
		return nil
	}

	if challenge.Type != "auth_required" {
		return fmt.Errorf("unexpected challenge type from server: %s", challenge.Type)
	}

	needsTOTP := false
	for _, m := range challenge.Methods {
		if m == "totp" {
			needsTOTP = true
			break
		}
	}

	if !needsTOTP {
		return fmt.Errorf("server required unknown authentication methods: %v", challenge.Methods)
	}

	code := strings.TrimSpace(totpPasscode)
	if code == "" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("server requires TOTP authentication, but --totp was not provided and stdin is not interactive")
		}
		fmt.Fprint(os.Stderr, "Server requires TOTP authentication.\nEnter TOTP passcode: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read TOTP passcode from stdin: %w", err)
		}
		code = strings.TrimSpace(line)
	}

	if err := sendAuthMessage(conn, AuthResponse{Type: "auth", TOTP: code}); err != nil {
		return fmt.Errorf("failed to send auth response: %w", err)
	}

	var res AuthResult
	if err := readAuthMessage(conn, &res); err != nil {
		return fmt.Errorf("failed to read auth result from server: %w", err)
	}

	if res.Status != "ok" {
		return fmt.Errorf("authentication failed: %s", res.Message)
	}

	return nil
}
