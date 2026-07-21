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
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"github.com/hashicorp/yamux"
	"golang.org/x/term"
)

type winsize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
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

	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/sh"
	}

	cmd := exec.Command(shellPath)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		slog.Error("Failed to start PTY", "error", err)
		return
	}
	defer ptmx.Close()

	// Listen for remote resize commands
	go func() {
		scanner := bufio.NewScanner(controlStream)
		for scanner.Scan() {
			var size winsize
			if err := json.Unmarshal(scanner.Bytes(), &size); err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
			}
		}
	}()

	go func() {
		CopyWithTrace(ptmx, dataStream, "remote->pty")
	}()
	CopyWithTrace(dataStream, ptmx, "pty->remote")

	cmd.Wait()
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
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGWINCH)
		for {
			select {
			case <-sigChan:
				if width, height, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
					fmt.Fprintf(controlStream, `{"cols": %d, "rows": %d}`+"\n", width, height)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		CopyWithTrace(dataStream, os.Stdin, "stdin->remote")
	}()
	CopyWithTrace(os.Stdout, dataStream, "remote->stdout")
}
