//go:build windows

package socket

import "os"

// reserveDataStdout is a no-op on Windows. The descriptor juggling its Unix counterpart
// does exists for the initramfs LUKS unlock flow, which does not apply here.
func reserveDataStdout() *os.File {
	return os.Stdout
}
