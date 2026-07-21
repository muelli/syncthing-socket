//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

func handleTerminalResize(ctx context.Context, controlStream io.Writer) {
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
}
