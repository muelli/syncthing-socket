//go:build !windows

package socket

import (
	"os"

	"golang.org/x/sys/unix"
)

// reserveDataStdout hands back a file for payload bytes and re-points file descriptor 1 at
// stderr, so that anything else writing to stdout cannot corrupt the payload.
//
// This is not defensive programming for its own sake. In the LUKS unlock flow a shell
// captures our stdout with $(...) and feeds it to cryptsetup as a passphrase, so a single
// stray line makes the key wrong. Two separate things have already done exactly that: our
// own connection banner, and syncthing's logger package, which hardcodes os.Stdout at
// package-init time (vendor/github.com/syncthing/syncthing/lib/logger/logger.go) and so
// prints "Joined relay ..." straight into the key.
//
// Reassigning os.Stdout does not help, because that logger captured the *os.File before we
// ever run. Redirecting the descriptor underneath it does: writes aimed at fd 1 land on
// stderr, while the duplicate we keep still refers to the real stdout.
func reserveDataStdout() *os.File {
	dataFD, err := unix.Dup(int(os.Stdout.Fd()))
	if err != nil {
		// Nothing safe to do here; carry on with the shared stdout.
		return os.Stdout
	}
	unix.CloseOnExec(dataFD)
	if err := unix.Dup2(int(os.Stderr.Fd()), int(os.Stdout.Fd())); err != nil {
		unix.Close(dataFD)
		return os.Stdout
	}
	return os.NewFile(uintptr(dataFD), "stdout-data")
}
