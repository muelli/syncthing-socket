# AGENTS.md

Welcome to the `syncthing-socket` project! This file serves as a brain-dump and onboarding document for AI agents (and human contributors). Please read this file to understand the architectural quirks, testing strategies, and build rules of this codebase.

## 1. Project Overview
`syncthing-socket` is a Go-based CLI tool that establishes Peer-to-Peer (P2P) connections across NAT using Syncthing's global relay and discovery infrastructure. 

**Core Dependencies:**
- `github.com/syncthing/syncthing/lib/relay/client`: Used for discovering peers and establishing the initial connection over Syncthing relays.
- `github.com/pion/webrtc/v3`: Used to establish direct P2P data channels (via ICE negotiation), bypassing the relay for faster throughput.
- `github.com/hashicorp/yamux`: Used for multiplexing multiple logical streams over a single connection.

## 2. Features & Architecture

### Connection Flow
1. The Server connects to the Syncthing relay network and announces itself.
2. The Client connects to the Server via the relay.
3. Both sides negotiate WebRTC ICE over the relay connection.
4. If a direct P2P connection succeeds, the relay is dropped, and traffic flows over WebRTC.

### Operational Modes
- **Raw Pipe**: By default, connects `stdin`/`stdout` between client and server.
- **SOCKS5 Proxy (`--socks`)**: The client runs a local SOCKS5 server and tunnels traffic over the P2P connection to the server, which acts as the exit node.
- **Remote Shell (`--shell`)**: 
  - **Unix**: Uses native PTYs (`github.com/creack/pty`) and dynamically propagates window resize events.
  - **Windows**: Uses a "Fake PTY" wrapper (`shell_pty_windows.go`) that locally echoes input to the client and translates Unix `\r` to Windows `\r\n` (standard Windows pipes lack PTY echo and line-editing).
- **Remote Command (`--command`)**: Executes specific commands remotely and pipes I/O.

## 3. Development & Build Rules

### Build System
- We use a `Justfile` instead of Make. 
- **Go Binary**: The `Justfile` is configured to look for a vendored Go binary at `./local/go/bin/go` first, falling back to the system `go`.

### Cross-Platform Compliance
- The code strictly supports compilation for Linux, macOS, and Windows.
- Always use `//go:build windows` and `//go:build !windows` tags for OS-specific features (e.g., PTY handling).

### Version String Logic
- To prevent discrepancies between local builds and CI, version extraction logic is strictly centralized in `scripts/version.sh`.
- The `Justfile` and `.github/workflows/build.yml` both invoke this script. DO NOT duplicate git versioning logic elsewhere.

## 4. Testing Strategies & Gotchas

### E2E Test Flakiness (CRITICAL)
- End-to-End tests in `e2e_*_test.go` **MUST** bypass the global relay network to prevent slow tests, rate-limiting, and timeouts.
- When spawning test servers/clients, always pass `--direct-port <PORT>`, `--relay ""`, and `--discovery ""`. 
- **Exception**: `TestHTTPProxyRouting` (in `e2e_proxy_test.go`) intentionally connects to the global network to test `HTTP_PROXY` support. It has a bumped timeout of 60s to accommodate network latency.

### Untracked Test Binaries
- Running `go test ./...` generates compiled binaries like `test-command-binary`, `test-proxy-binary`, etc. 
- These are already covered by `.gitignore` (`test-*`), but be aware they are dropped in the root directory during testing.

## 5. CI/CD Pipeline
- **Triggers**: GitHub Actions (`build.yml`) builds and publishes releases on any tag starting with `v*` (e.g., `v43`, `v1.2.3`).
- **Permissions**: The build workflow relies on `permissions: contents: write` to allow the GitHub Actions bot to create releases and upload binary assets.
