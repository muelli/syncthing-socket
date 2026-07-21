package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"

	"github.com/hashicorp/yamux"
	"golang.org/x/term"
)

type winsize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type ShellSession interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
	Wait() error
	Resize(rows, cols uint16) error
}

func runShellServer(conn net.Conn) {
	slog.Info("Starting yamux multiplexer and PTY Shell Server")
	session, err := yamux.Server(conn, nil)
	if err != nil {
		slog.Error("Failed to start yamux server", "error", err)
		return
	}
	defer session.Close()

	dataStream, err := session.Accept()
	if err != nil {
		slog.Error("Failed to accept data stream", "error", err)
		return
	}
	controlStream, err := session.Accept()
	if err != nil {
		slog.Error("Failed to accept control stream", "error", err)
		return
	}

	shellSession, err := spawnShell()
	if err != nil {
		slog.Error("Failed to start shell", "error", err)
		return
	}
	defer shellSession.Close()

	// Listen for remote resize commands
	go func() {
		scanner := bufio.NewScanner(controlStream)
		for scanner.Scan() {
			var size winsize
			if err := json.Unmarshal(scanner.Bytes(), &size); err == nil {
				shellSession.Resize(size.Rows, size.Cols)
			}
		}
	}()

	go func() {
		CopyWithTrace(shellSession, dataStream, "remote->pty")
	}()
	CopyWithTrace(dataStream, shellSession, "pty->remote")

	shellSession.Wait()
}

func runShellClient(ctx context.Context, p2pConn net.Conn) {
	slog.Info("Starting interactive shell client")

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		slog.Warn("Failed to set terminal to raw mode (not a TTY?)", "error", err)
	} else {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	session, err := yamux.Client(p2pConn, nil)
	if err != nil {
		slog.Error("Failed to start yamux client", "error", err)
		return
	}
	defer session.Close()

	dataStream, err := session.Open()
	if err != nil {
		slog.Error("Failed to open data stream", "error", err)
		return
	}

	controlStream, err := session.Open()
	if err != nil {
		slog.Error("Failed to open control stream", "error", err)
		return
	}

	// Send initial size
	if width, height, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		fmt.Fprintf(controlStream, `{"cols": %d, "rows": %d}`+"\n", width, height)
	}

	// Handle Terminal Resizes
	go handleTerminalResize(ctx, controlStream)

	go func() {
		CopyWithTrace(dataStream, os.Stdin, "stdin->remote")
	}()
	CopyWithTrace(os.Stdout, dataStream, "remote->stdout")
}

func runCommandServer(conn net.Conn, commandStr string) {
	slog.Info("Starting command execution server", "command", commandStr)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/c", commandStr)
	} else {
		cmd = exec.Command("/bin/sh", "-c", commandStr)
	}
	
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		slog.Error("Failed to create stdin pipe", "error", err)
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("Failed to create stdout pipe", "error", err)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		slog.Error("Failed to create stderr pipe", "error", err)
		return
	}

	// Route stderr to our structured logging system
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			slog.Warn("Command stderr", "output", scanner.Text())
		}
	}()

	go func() {
		CopyWithTrace(stdinPipe, conn, "remote->stdin")
		stdinPipe.Close()
	}()

	go func() {
		CopyWithTrace(conn, stdoutPipe, "stdout->remote")
	}()

	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start command", "error", err)
		return
	}

	err = cmd.Wait()
	if err != nil {
		slog.Info("Command exited with error", "error", err)
	} else {
		slog.Info("Command exited successfully")
	}
}
