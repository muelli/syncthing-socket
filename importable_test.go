// SPDX-License-Identifier: AGPL-3.0-or-later

package socket

import (
	"os/exec"
	"strings"
	"testing"
)

// TestImportablePackagesUseOnlyTheStandardLibrary keeps discovery and identity light.
// Other projects import them precisely so that they do not inherit cobra, pion/webrtc, or
// the Syncthing libraries; one stray import here would quietly undo that for all of them.
func TestImportablePackagesUseOnlyTheStandardLibrary(t *testing.T) {
	own := map[string]bool{
		"github.com/muelli/syncthing-socket/discovery": true,
		"github.com/muelli/syncthing-socket/identity":  true,
	}
	out, err := exec.Command("go", "list", "-deps",
		"-f", "{{if not .Standard}}{{.ImportPath}}{{end}}",
		"./discovery", "./identity").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !own[pkg] {
			t.Errorf("discovery or identity pulls in %s, which is outside the standard library", pkg)
		}
	}
}
