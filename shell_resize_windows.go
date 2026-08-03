//go:build windows

package socket

import (
	"context"
	"io"
)

func handleTerminalResize(ctx context.Context, controlStream io.Writer) {
	// No SIGWINCH on Windows, do nothing
	<-ctx.Done()
}
