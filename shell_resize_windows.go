//go:build windows

package main

import (
	"context"
	"io"
)

func handleTerminalResize(ctx context.Context, controlStream io.Writer) {
	// No SIGWINCH on Windows, do nothing
	<-ctx.Done()
}
