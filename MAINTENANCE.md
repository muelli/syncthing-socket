# Maintenance Guide

This document outlines routine maintenance tasks for the `syncthing-socket` project.

## Updating Dependencies

The project relies on standard Go modules, but it vendors all dependencies into the `vendor/` directory to ensure reproducible builds and offline compilation.

### When to update dependencies
1. **Security Vulnerabilities:** If a CVE is announced in an upstream package (like `pion/webrtc`, `yamux`, or `syncthing`), patch it immediately.
2. **Bug Fixes / New Features:** When a library releases a fix for a bug you are experiencing, or adds a feature you want to leverage.
3. **Routine Maintenance:** Perform a routine update every few months so the project doesn't fall too far behind, which makes major version upgrades much harder later on.

### How to update dependencies
Because this project utilizes a local Go binary installation (`./local/go/bin/go`), use it for the following commands. If you have Go installed globally, you can substitute `go` for `./local/go/bin/go`.

**1. See what updates are available:**
```bash
./local/go/bin/go list -u -m all
```

**2. Update the dependencies:**
- To update a **specific** package to the latest version:
  ```bash
  ./local/go/bin/go get github.com/syncthing/syncthing@latest
  ```
- To update **all** packages to their latest minor/patch versions:
  ```bash
  ./local/go/bin/go get -u ./...
  ```

**3. Tidy your modules:**
This removes any unused dependencies from your `go.mod` file and ensures `go.sum` is accurate.
```bash
./local/go/bin/go mod tidy
```

**4. Sync the Vendor Directory (CRITICAL):**
Because this repository stores its dependencies locally, you **must** run this command after updating. Otherwise, CI and local builds will fail complaining about inconsistent vendoring.
```bash
./local/go/bin/go mod vendor
```

**5. Test and Commit:**
Run the test suite to ensure the upstream changes didn't break the P2P tunnels or PTY shells:
```bash
./local/go/bin/go test -v ./...
git add go.mod go.sum vendor/
git commit -m "chore: update dependencies"
```

## Simulating GitHub CI Locally

You can test the GitHub Actions build pipeline (`.github/workflows/build.yml`) locally without pushing commits by using [`act`](https://nektosact.com/).

Since this project uses `podman`, ensure your user socket is running and configure `act` to use it:

```bash
# Ensure the rootless podman socket is active
systemctl --user enable --now podman.socket

# Export DOCKER_HOST so act uses podman
export DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock

# Run the CI pipeline locally
~/.local/bin/act
```

## Technical Debt & Known Upstream Issues

### Go 1.22 Pin (Android compilation)
The project is currently pinned to **Go 1.22.0** (both in `go.mod` and `.github/workflows/build.yml`). 
This is a stopgap workaround due to an upstream dependency issue that breaks Android cross-compilation on Go 1.23+.

- **The Issue:** The networking library `github.com/wlynxg/anet` (a transitive dependency via `github.com/pion/ice`) uses the `go:linkname` compiler directive to access `net.zoneCache`, a private standard library variable. Starting in Go 1.23, the Go authors restricted `go:linkname` for unexported standard library symbols. This causes `gomobile bind` to fail with `invalid reference to net.zoneCache` during the C-Shared linking phase of the Android build.
- **Monitoring Upstream:** You can periodically check if this issue has been resolved upstream by monitoring:
  - `pion/ice` GitHub issues: [https://github.com/pion/ice/issues](https://github.com/pion/ice/issues)
  - `wlynxg/anet` GitHub issues: [https://github.com/wlynxg/anet/issues](https://github.com/wlynxg/anet/issues)
- **Resolution Path:** Once `pion/ice` removes the dependency or updates to a fixed version of `anet`, you can bump the Go version:
  1. `go get -u github.com/pion/ice/v4@latest`
  2. Change `go 1.22.0` to `go 1.25.0` (or later) in `go.mod`.
  3. Change `go-version: '1.22'` to `go-version: '1.25.0'` in `.github/workflows/build.yml`.
