# Syncthing Socket

A netcat-like client/server utility that allows two endpoints to communicate bi-directionally even when both are behind a NAT, leveraging the public **Syncthing Relay Network** and **Global Discovery Directory**.

## Key Features
- **P2P NAT Traversal:** Automatically attempts to punch holes through NATs using WebRTC **ICE** DataChannels. Seamlessly falls back to Syncthing's global public relay network if a direct connection is impossible.
- **End-to-End Encryption:** Automatically establishes a secure end-to-end tunnel using **TLS 1.3**.
- **Cryptographic Device Verification:** Identifies and authenticates endpoints by hashing their TLS certificates to generate unique Syncthing **Device IDs**, preventing man-in-the-middle attacks.
- **Global Discovery:** Server registers itself dynamically in the Syncthing global announce directory (`discovery.syncthing.net`), allowing clients to look up and connect to the server using only its Device ID.
- **Interactive Shell:** Built-in pseudo-terminal (PTY) shell mode (`-shell`) natively replaces SSH, perfectly forwarding terminal sizes for interactive tools like `vim` or `htop`.
- **SOCKS5 Proxy:** Built-in multiplexed remote SOCKS proxy (`-socks`).
- **Netcat-style:** Pipes standard input (`stdin`) and standard output (`stdout`) bi-directionally between endpoints.

---

## Compilation

You can build the binary using:

```bash
go build -o syncthing-socket main.go
```

---

## Usage

### 1. Start the Server
Start the server listening on the Syncthing relay network. By default, it will dynamically choose a low-latency public relay from the pool, print the connection string, and announce itself to the Syncthing Global Discovery servers:

```bash
./syncthing-socket server
```

Example Output:
```text
==================================================
Server Device ID: YGOV2KW-QIGHJYZ-BCA52RN-7EQTGY3-CQGARMC-3JVUWAA-BXHSKI5-ZRJSBQT
==================================================
Connected to Relay: relay://195.22.153.71:22067/?id=SPXEVID...
To connect, run:
  ./syncthing-socket client -relay "relay://195.22.153.71:22067/?id=SPXEVID..." YGOV2KW-QIGHJYZ-BCA52RN-7EQTGY3-CQGARMC-3JVUWAA-BXHSKI5-ZRJSBQT
Or simply:
  ./syncthing-socket client YGOV2KW-QIGHJYZ-BCA52RN-7EQTGY3-CQGARMC-3JVUWAA-BXHSKI5-ZRJSBQT@relay://195.22.153.71:22067/?id=SPXEVID...
==================================================
2026/07/11 13:16:33 Announcing availability to https://discovery-announce-v4.syncthing.net/v2/?nolookup...
2026/07/11 13:16:33 Successfully announced to https://discovery-announce-v4.syncthing.net/v2/?nolookup
```

#### Server Options:
- `-shell`: Start a fully interactive PTY shell daemon (replaces SSH).
- `-socks`: Start a remote SOCKS5 proxy server.
- `-passphrase <secret>`: Deterministically generates a secure TLS certificate and Device ID from a secret password, so you don't need to copy-paste long Device IDs.
- `-cert <path>`: Custom TLS certificate path (default: empty, runs in-memory).
- `-key <path>`: Custom TLS key path (default: empty, runs in-memory).
- `-relay <uri>`: Specific relay URI, or a dynamic pool URL (default: `dynamic+https://relays.syncthing.net/endpoint`).
- `-discovery <urls>`: Comma-separated list of global announce directories.
- `-direct-port <port>`: Enable direct TCP connection listening on this port (0 to disable, default: 0).
- `-log-level <level>`: Logging level: trace, debug, info, warn, error (default: info).
- `-log-format <format>`: Logging format: auto, text, json, journald (default: auto).

---

### 2. Connect with the Client

You can connect to the server in three ways:

#### Option A: Dynamic Discovery Lookup (Recommended)
If you only know the Server's Device ID, the client will automatically query the global directory, resolve which relay the server is currently on, and connect to it:

```bash
./syncthing-socket client <SERVER_DEVICE_ID>
```

#### Option B: Combined Connection String
Connect directly using the connection string printed by the server:

```bash
./syncthing-socket client <SERVER_DEVICE_ID>@<RELAY_URI>
```

#### Option C: Bypassing Discovery via Flags
Specify the relay address manually via the `-relay` flag:

```bash
./syncthing-socket client -relay "<RELAY_URI>" <SERVER_DEVICE_ID>
```

#### Client Options:
- `-shell`: Put the local terminal in raw mode and connect to a remote `-shell` server.
- `-socks <address>`: Spin up a local SOCKS5 proxy on this address (e.g. `127.0.0.1:10800`).
- `-passphrase <secret>`: Use the same passphrase as the server to auto-discover and connect without needing the Server Device ID!
- `-cert <path>` / `-key <path>`: Use a persistent certificate (default: generates a secure in-memory certificate).
- `-discovery <url>`: Custom discovery server URL for lookups (default: `https://discovery-lookup.syncthing.net/v2/`).
- `-direct`: Attempt direct P2P connections via WebRTC ICE (UDP NAT Hole Punching) and direct TCP before seamlessly falling back to relay. Set `-direct=false` to disable ICE and force relay connections (default: true).
- `-log-level <level>`: Logging level: trace, debug, info, warn, error (default: info).
- `-log-format <format>`: Logging format: auto, text, json, journald (default: auto).

---

## Advanced: Interactive PTY Shell (SSH Replacement)

You can completely bypass the need for an external OpenSSH server by using the built-in PTY shell!

1. **On the Server:**
```bash
./syncthing-socket server -passphrase "my-secret" -shell
```
2. **On the Client:**
```bash
./syncthing-socket client -passphrase "my-secret" -shell
```
This spawns a remote bash session and pipes your raw local terminal directly into it. It natively supports tab-completion, `vim`, `htop`, and even transmits window resizing events (`SIGWINCH`) over a dedicated lightweight control stream so the remote UI always perfectly fits your screen.

---

## Advanced: SSH Port Forwarding

You can front your local SSH daemon (or any other TCP service) using `syncthing-socket`. This allows you to securely SSH into a machine behind a NAT.

### 1. On the Server (SSH Host)
Start the server with the `-forward` option pointing to your SSH port (usually `127.0.0.1:22`). You should also specify `-cert` and `-key` so that your server keeps a persistent Device ID:

```bash
./syncthing-socket server -cert cert.pem -key key.pem -forward 127.0.0.1:22
```

The server will print its Device ID (e.g. `SERVER_DEVICE_ID`). It runs as a persistent daemon and forwards incoming connections to the local SSH port in separate goroutines, handling multiple concurrent sessions.

### 2. On the Client
On the client machine, connect using standard SSH with the `-o ProxyCommand` flag:

```bash
ssh -o ProxyCommand="./syncthing-socket client <SERVER_DEVICE_ID>" user@ignored_host
```

Alternatively, add an entry to your local `~/.ssh/config` file:

```text
Host my-nat-server
    User your_username
    ProxyCommand /path/to/syncthing-socket client <SERVER_DEVICE_ID>
```

Then connect simply by running:
```bash
ssh my-nat-server
```

---

## Proxy Support

Both the `server` and `client` natively respect standard proxy environment variables to help bypass restrictive firewalls.

**Supported Variables:**
- `SOCKS_PROXY` (or `socks_proxy`): e.g. `socks5://127.0.0.1:1080`
- `HTTP_PROXY` (or `http_proxy`): e.g. `http://proxy.corp.com:8080`
- `HTTPS_PROXY` (or `https_proxy`)

**How it works:**
1. **Discovery (HTTPS):** Uses standard Go networking logic to route queries to `discovery.syncthing.net` via your specified `HTTP_PROXY` / `HTTPS_PROXY`.
2. **Syncthing Relay Network (Raw TCP):** Transparently dials out to the Syncthing Relay endpoints (`relay://...`) by tunneling the raw TCP traffic through either your specified `SOCKS_PROXY` or via an `HTTP CONNECT` tunnel if an `HTTP_PROXY` is provided.
3. **Direct Connections (WebRTC ICE):** STUN/TURN discovery queries are handled by Pion and may also route over supported proxies if configured in the environment. Direct P2P TCP hole punching attempts to establish a connection directly between peers (bypassing proxies if local).

*Note: If both SOCKS and HTTP proxies are provided in the environment, SOCKS takes precedence for raw TCP routing to the Relay network.*

---

## Security Model

1. **Relay Blindness:** The relay server acts purely as a TCP proxy. It cannot decrypt or read any data sent through it.
2. **E2E TLS 1.3:** Once the relay session is joined by both client and server, they negotiate a direct TLS 1.3 handshake.
3. **Peer Verification:** 
   - The Client extracts the leaf certificate from the TLS handshake, hashes it to generate the server's Device ID, and compares it against the expected `<SERVER_DEVICE_ID>`. The connection is terminated immediately if they do not match.
   - The Server requests the client's certificate to verify and log the client's Device ID.
