//go:build !linux

package socket

import (
	"fmt"
	"os/exec"
)

// runLUKSAgent only means anything inside a Linux initramfs. The command stays registered
// everywhere so that `--help` and the completions are identical across platforms.
func runLUKSAgent() error {
	return fmt.Errorf("the LUKS password agent is only available on Linux")
}

// setChildProcAttrs has no counterpart off Linux, where the agent does not run.
func setChildProcAttrs(*exec.Cmd) {}
