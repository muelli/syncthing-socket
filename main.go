package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	flag "github.com/spf13/pflag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/relay/client"
	"github.com/syncthing/syncthing/lib/tlsutil"
)

// Logging setup is in logging.go

var Version = "dev"

func isTraceEnabled() bool {
	return slog.Default().Handler().Enabled(context.Background(), LevelTrace)
}

func main() {
	setupProxyEnvironment()

	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	serverCert := serverCmd.String("cert", "", "Path to TLS certificate (optional)")
	serverKey := serverCmd.String("key", "", "Path to TLS key (optional)")
	serverPassphrase := serverCmd.String("passphrase", "", "Passphrase to deterministically generate the TLS certificate")
	serverSocks := serverCmd.Bool("socks", false, "Start a remote SOCKS5 server handling multiplexed connections")
	serverShell := serverCmd.Bool("shell", false, "Start an interactive PTY shell server")
	serverCommand := serverCmd.String("command", "", "Command to execute and pipe stdout/stdin for each incoming connection")
	serverRelay := serverCmd.String("relay", "dynamic+https://relays.syncthing.net/endpoint", "Relay URI or dynamic pool URL")
	serverDiscovery := serverCmd.String("discovery", "https://discovery-announce-v4.syncthing.net/v2/?nolookup,https://discovery-announce-v6.syncthing.net/v2/?nolookup", "Comma-separated discovery announce URLs")
	serverForward := serverCmd.String("forward", "", "Forward incoming connections to this host:port (e.g. 127.0.0.1:22)")
	serverDirectPort := serverCmd.Int("direct-port", 0, "Enable direct TCP connection listening on this port (0 to disable)")
	serverLogLevel := serverCmd.String("log-level", "info", "Log level (trace, debug, info, warn, error)")
	serverLogFormat := serverCmd.String("log-format", "auto", "Log format (auto, text, json, journald)")

	clientCmd := flag.NewFlagSet("client", flag.ExitOnError)
	clientCert := clientCmd.String("cert", "", "Path to TLS certificate (optional)")
	clientKey := clientCmd.String("key", "", "Path to TLS key (optional)")
	clientPassphrase := clientCmd.String("passphrase", "", "Passphrase to deterministically generate the TLS certificate")
	clientSocks := clientCmd.String("socks", "", "Start a local SOCKS5 proxy on this address (e.g. 127.0.0.1:1080)")
	clientShell := clientCmd.Bool("shell", false, "Start an interactive PTY shell client")
	clientRelay := clientCmd.String("relay", "", "Relay URI (if specified, bypasses discovery lookup)")
	clientDiscovery := clientCmd.String("discovery", "https://discovery-lookup.syncthing.net/v2/", "Discovery lookup URL")
	clientTryDirect := clientCmd.Bool("direct", true, "Try direct TCP connections before falling back to relay")
	clientLogLevel := clientCmd.String("log-level", "info", "Log level (trace, debug, info, warn, error)")
	clientLogFormat := clientCmd.String("log-format", "auto", "Log format (auto, text, json, journald)")

	idCmd := flag.NewFlagSet("id", flag.ExitOnError)
	idPassphrase := idCmd.String("passphrase", "", "Passphrase to compute the Syncthing ID for")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "version" {
		fmt.Printf("syncthing-socket version %s\n", Version)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch os.Args[1] {
	case "server":
		serverCmd.Parse(os.Args[2:])
		setupLogging(*serverLogLevel, *serverLogFormat)

		if (*serverSocks && *serverShell) || (*serverSocks && *serverForward != "") || (*serverSocks && *serverCommand != "") || (*serverShell && *serverForward != "") || (*serverShell && *serverCommand != "") || (*serverForward != "" && *serverCommand != "") {
			slog.Error("Error: --socks, --shell, --command, and --forward are mutually exclusive. Please specify only one mode.")
			os.Exit(1)
		}

		var cert tls.Certificate
		var err error
		if *serverPassphrase != "" {
			cert, err = generateDeterministicCert(*serverPassphrase + "server")
		} else if *serverCert != "" && *serverKey != "" {
			cert, err = loadOrGenerateCert(*serverCert, *serverKey)
		} else if *serverCert == "" && *serverKey == "" {
			cert, err = tlsutil.NewCertificateInMemory("syncthing-socket-server", 365)
		} else {
			slog.Error("Error: both -cert and -key must be specified if one is provided")
			os.Exit(1)
		}
		if err != nil {
			slog.Error("Error getting server cert", "error", err)
			os.Exit(1)
		}
		var discoveryServers []string
		if *serverDiscovery != "" {
			for _, ds := range strings.Split(*serverDiscovery, ",") {
				ds = strings.TrimSpace(ds)
				if ds != "" {
					discoveryServers = append(discoveryServers, ds)
				}
			}
		}
		if err := runServer(ctx, cert, *serverRelay, discoveryServers, *serverForward, *serverDirectPort, *serverSocks, *serverShell, *serverCommand); err != nil {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}

	case "client":
		clientCmd.Parse(os.Args[2:])
		setupLogging(*clientLogLevel, *clientLogFormat)

		if *clientSocks != "" && *clientShell {
			slog.Error("Error: -socks and -shell are mutually exclusive. Please specify only one mode.")
			os.Exit(1)
		}

		args := clientCmd.Args()
		var targetStr string
		if *clientPassphrase != "" {
			if len(args) > 0 {
				targetStr = args[0]
			}
		} else {
			if len(args) < 1 {
				fmt.Println("Error: client mode requires target server Device ID unless --passphrase is used")
				clientCmd.Usage()
				os.Exit(1)
			}
			targetStr = args[0]
		}

		var serverID string
		var relayURI string

		if targetStr != "" && strings.Contains(targetStr, "@") {
			parts := strings.SplitN(targetStr, "@", 2)
			serverID = parts[0]
			relayURI = parts[1]
		} else if targetStr != "" {
			serverID = targetStr
			relayURI = *clientRelay
		} else {
			relayURI = *clientRelay
		}

		if *clientPassphrase != "" {
			serverCert, _ := generateDeterministicCert(*clientPassphrase + "server")
			derivedID := syncthingprotocol.NewDeviceID(serverCert.Certificate[0]).String()
			if serverID == "" {
				serverID = derivedID
			} else if serverID != derivedID {
				slog.Warn("Provided Server ID does not match the passphrase-derived Server ID", "provided", serverID, "derived", derivedID)
			}
		}

		var cert tls.Certificate
		var err error
		if *clientPassphrase != "" {
			cert, err = generateDeterministicCert(*clientPassphrase + "client")
		} else if *clientCert != "" && *clientKey != "" {
			cert, err = loadOrGenerateCert(*clientCert, *clientKey)
		} else if *clientCert == "" && *clientKey == "" {
			cert, err = tlsutil.NewCertificateInMemory("syncthing-socket-client", 365)
		} else {
			slog.Error("Error: both -cert and -key must be specified if one is provided")
			os.Exit(1)
		}
		if err != nil {
			slog.Error("Error getting client cert", "error", err)
			os.Exit(1)
		}

		if err := runClient(ctx, serverID, relayURI, cert, *clientDiscovery, *clientTryDirect, *clientSocks, *clientShell); err != nil {
			slog.Error("Client error", "error", err)
			os.Exit(1)
		}

	case "id":
		idCmd.Parse(os.Args[2:])
		if *idPassphrase == "" {
			fmt.Println("Error: --passphrase is required")
			os.Exit(1)
		}
		
		serverCert, err := generateDeterministicCert(*idPassphrase + "server")
		if err != nil {
			fmt.Println("Error generating server cert:", err)
			os.Exit(1)
		}
		serverID := syncthingprotocol.NewDeviceID(serverCert.Certificate[0])
		
		clientCert, err := generateDeterministicCert(*idPassphrase + "client")
		if err != nil {
			fmt.Println("Error generating client cert:", err)
			os.Exit(1)
		}
		clientID := syncthingprotocol.NewDeviceID(clientCert.Certificate[0])
		
		fmt.Printf("Server ID: %s\n", serverID.String())
		fmt.Printf("Client ID: %s\n", clientID.String())
		os.Exit(0)

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: syncthing-socket <command> [options]")
	fmt.Println("Commands:")
	fmt.Println("  server    Start the listening server")
	fmt.Println("  client    Connect to a server")
	fmt.Println("  id        Compute Device IDs from a passphrase")
	fmt.Println("Run 'syncthing-socket <command> -h' for options.")
}

func loadOrGenerateCert(certPath, keyPath string) (tls.Certificate, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}
	slog.Info("Generating new TLS certificate", "cert", certPath, "key", keyPath)
	return tlsutil.NewCertificate(certPath, keyPath, "syncthing-socket", 3650)
}

func announce(ctx context.Context, cert tls.Certificate, addresses []string, discoveryServers []string) {
	type AnnouncePayload struct {
		Addresses []string `json:"addresses"`
	}
	payload := AnnouncePayload{
		Addresses: addresses,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal announce payload", "error", err)
		return
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}

	for _, ds := range discoveryServers {
		go func(ds string) {
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()

			for {
				slog.Debug("Announcing availability to discovery server", "ds", ds, "addresses", addresses)
				req, err := http.NewRequestWithContext(ctx, "POST", ds, strings.NewReader(string(body)))
				if err != nil {
					slog.Error("Failed to create announce request", "ds", ds, "error", err)
				} else {
					req.Header.Set("Content-Type", "application/json")
					resp, err := client.Do(req)
					if err != nil {
						slog.Error("Announce request failed", "ds", ds, "error", err)
					} else {
						resp.Body.Close()
						if resp.StatusCode == http.StatusNoContent {
							slog.Info("Successfully announced availability", "ds", ds)
						} else {
							slog.Warn("Announce returned non-204 status", "ds", ds, "status", resp.StatusCode)
						}
					}
				}

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(ds)
	}
}

func lookup(ctx context.Context, serverID string, discoveryServer string) ([]string, error) {
	urlStr := fmt.Sprintf("%s&device=%s", discoveryServer, serverID)
	if !strings.Contains(discoveryServer, "?") {
		urlStr = fmt.Sprintf("%s?device=%s", discoveryServer, serverID)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}

	slog.Debug("Looking up server address on discovery server", "ds", discoveryServer, "serverID", serverID)
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("device %s not found in discovery", serverID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery lookup returned HTTP %d", resp.StatusCode)
	}

	type LookupResponse struct {
		Addresses []string `json:"addresses"`
	}
	var lr LookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}

	return lr.Addresses, nil
}

func runServer(ctx context.Context, cert tls.Certificate, relayURI string, discoveryServers []string, forwardAddr string, directPort int, isSocks bool, isShell bool, isCommand string) error {
	u, err := url.Parse(relayURI)
	if err != nil {
		return fmt.Errorf("invalid relay URI: %w", err)
	}

	serverID := syncthingprotocol.NewDeviceID(cert.Certificate[0])
	fmt.Println("==================================================")
	fmt.Printf("Server Device ID: %s\n", serverID)
	fmt.Println("==================================================")
	slog.Info("Server Device ID computed", "id", serverID.String())

	var tcpListener net.Listener
	if directPort > 0 {
		tcpAddr := fmt.Sprintf(":%d", directPort)
		tcpListener, err = net.Listen("tcp", tcpAddr)
		if err != nil {
			return fmt.Errorf("failed to start direct TCP listener: %w", err)
		}
		defer tcpListener.Close()

		actualPort := tcpListener.Addr().(*net.TCPAddr).Port
		slog.Info("Direct TCP listener started", "port", actualPort)
		fmt.Printf("Direct TCP Port: %d\n", actualPort)
		fmt.Println("==================================================")
	}

	slog.Info("Relay client starting", "relay", u.String())
	relayClient, err := client.NewClient(u, []tls.Certificate{cert}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("failed to create relay client: %w", err)
	}

	systemdNotify("STATUS=Connecting to relay...")

	go func() {
		if err := relayClient.Serve(ctx); err != nil {
			slog.Error("Relay client stopped", "error", err)
		}
	}()

	var connectedURI *url.URL
	for {
		if err := relayClient.Error(); err != nil {
			return fmt.Errorf("relay client error: %w", err)
		}
		connectedURI = relayClient.URI()
		if connectedURI != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	extraFlags := ""
	if isShell {
		extraFlags = " --shell"
	} else if isSocks {
		extraFlags = " --socks 127.0.0.1:1080"
	}

	fmt.Printf("Connected to Relay: %s\n", connectedURI.String())
	fmt.Printf("To connect, run:\n")
	fmt.Printf("  ./syncthing-socket client%s -relay %q %s\n", extraFlags, connectedURI.String(), serverID.String())
	fmt.Printf("Or simply:\n")
	fmt.Printf("  ./syncthing-socket client%s %s@%s\n", extraFlags, serverID.String(), connectedURI.String())
	fmt.Println("==================================================")
	slog.Info("Connected to relay", "uri", connectedURI.String())

	systemdNotify(fmt.Sprintf("READY=1\nSTATUS=Connected to %s", connectedURI.String()))

	var announceAddrs []string
	announceAddrs = append(announceAddrs, connectedURI.String())
	if directPort > 0 {
		actualPort := tcpListener.Addr().(*net.TCPAddr).Port
		announceAddrs = append(announceAddrs, fmt.Sprintf("tcp://:%d", actualPort))
	}

	if len(discoveryServers) > 0 {
		announce(ctx, cert, announceAddrs, discoveryServers)
	}

	if forwardAddr != "" {
		// Forwarding Mode: accept connections indefinitely from both channels
		if directPort > 0 {
			go func() {
				for {
					conn, err := tcpListener.Accept()
					if err != nil {
						slog.Error("Direct TCP listener stopped", "error", err)
						return
					}
					slog.Info("Accepted direct TCP connection", "remote", conn.RemoteAddr().String())
					go handleForwardConn(conn, cert, forwardAddr, false)
				}
			}()
		}

		invs := relayClient.Invitations()
		for {
			select {
			case inv, ok := <-invs:
				if !ok {
					return fmt.Errorf("invitations channel closed")
				}
				slog.Info("Received invitation from relay, joining session")
				conn, err := client.JoinSession(ctx, inv)
				if err != nil {
					slog.Error("Failed to join session", "error", err)
					continue
				}
				go handleForwardConn(conn, cert, forwardAddr, true)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	} else {
		// Simple Netcat Mode: accept exactly one connection and exit
		type connInfo struct {
			conn    net.Conn
			isRelay bool
		}
		connChan := make(chan connInfo, 1)

		if directPort > 0 {
			go func() {
				conn, err := tcpListener.Accept()
				if err != nil {
					return
				}
				select {
				case connChan <- connInfo{conn, false}:
					slog.Info("Accepted direct TCP connection", "remote", conn.RemoteAddr().String())
				default:
					conn.Close()
				}
			}()
		}

		go func() {
			invs := relayClient.Invitations()
			for {
				select {
				case inv, ok := <-invs:
					if !ok {
						return
					}
					slog.Info("Received invitation from relay, joining session")
					conn, err := client.JoinSession(ctx, inv)
					if err != nil {
						slog.Error("Failed to join session", "error", err)
						continue
					}
					select {
					case connChan <- connInfo{conn, true}:
						return
					default:
						conn.Close()
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		select {
		case info := <-connChan:
			handleServerConn(info.conn, cert, info.isRelay, isSocks, isShell, isCommand)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func handleServerConn(conn net.Conn, cert tls.Certificate, isRelay bool, isSocks bool, isShell bool, isCommand string) {
	defer conn.Close()

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		slog.Error("TLS handshake failed", "error", err)
		return
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		clientID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
		slog.Info("Connection established with client", "id", clientID.String())
	} else {
		slog.Info("Connection established with anonymous client")
	}

	handleConn := func(finalConn net.Conn) {
		if isSocks {
			runSocksServer(finalConn)
		} else if isShell {
			runShellServer(finalConn)
		} else if isCommand != "" {
			runCommandServer(finalConn, isCommand)
		} else {
			defer finalConn.Close()
			pipeBiDirectional(finalConn)
		}
	}

	if isRelay {
		p2pConn, fallback, err := negotiateWebRTCServer(context.Background(), tlsConn)
		if fallback || err != nil {
			slog.Info("Using relay connection for data (ICE bypassed or failed)", "error", err)
			handleConn(tlsConn)
			return
		}
		slog.Info("Direct P2P ICE connection established! Closing relay.")
		tlsConn.Close()
		handleConn(p2pConn)
	} else {
		handleConn(tlsConn)
	}
}

func runClient(ctx context.Context, serverIDStr string, relayURIOverride string, cert tls.Certificate, discoveryServer string, tryDirect bool, localSocks string, isShell bool) error {
	serverID, err := syncthingprotocol.DeviceIDFromString(serverIDStr)
	if err != nil {
		return fmt.Errorf("invalid server Device ID: %w", err)
	}

	var addresses []string
	if relayURIOverride != "" {
		addresses = []string{relayURIOverride}
	} else {
		slog.Info("Looking up server address on discovery server...")
		var err error
		addresses, err = lookup(ctx, serverID.String(), discoveryServer)
		if err != nil {
			return fmt.Errorf("discovery lookup failed: %w", err)
		}
		slog.Debug("Resolved addresses from discovery", "addresses", addresses)
	}

	var directAddresses []string
	var relayAddresses []string

	for _, addr := range addresses {
		if strings.HasPrefix(addr, "tcp://") {
			directAddresses = append(directAddresses, addr)
		} else if strings.HasPrefix(addr, "relay://") {
			relayAddresses = append(relayAddresses, addr)
		}
	}

	if tryDirect && len(directAddresses) > 0 {
		for _, addrStr := range directAddresses {
			slog.Info("Attempting direct TCP connection", "address", addrStr)
			u, err := url.Parse(addrStr)
			if err != nil {
				slog.Warn("Failed to parse direct address", "address", addrStr, "error", err)
				continue
			}

			dialer := net.Dialer{Timeout: 5 * time.Second}
			conn, err := dialer.DialContext(ctx, "tcp", u.Host)
			if err != nil {
				slog.Debug("Direct connection failed", "address", addrStr, "error", err)
				continue
			}

			tlsConn := tls.Client(conn, &tls.Config{
				Certificates:       []tls.Certificate{cert},
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
			})
			if err := tlsConn.Handshake(); err != nil {
				slog.Debug("Direct connection TLS handshake failed", "address", addrStr, "error", err)
				conn.Close()
				continue
			}

			state := tlsConn.ConnectionState()
			if len(state.PeerCertificates) == 0 {
				slog.Debug("Direct connection presented no certificates", "address", addrStr)
				tlsConn.Close()
				continue
			}
			peerID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
			if peerID != serverID {
				slog.Warn("Direct connection Device ID mismatch", "expected", serverID.String(), "got", peerID.String())
				tlsConn.Close()
				continue
			}

			slog.Info("Connected directly (bypassing relay)", "address", addrStr)
			handleClientConn(ctx, tlsConn, localSocks, isShell)
			return nil
		}
		slog.Info("All direct TCP connections failed, falling back to relay...")
	}

	if len(relayAddresses) == 0 {
		return fmt.Errorf("no connectable relay or TCP addresses found")
	}

	relayURI := relayAddresses[0]
	u, err := url.Parse(relayURI)
	if err != nil {
		return fmt.Errorf("invalid relay URI: %w", err)
	}

	slog.Info("Requesting session invitation from relay", "relay", u.String(), "serverID", serverID.String())
	invitation, err := client.GetInvitationFromRelay(ctx, u, serverID, []tls.Certificate{cert}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get invitation from relay: %w", err)
	}

	slog.Info("Joining relay session")
	conn, err := client.JoinSession(ctx, invitation)
	if err != nil {
		return fmt.Errorf("failed to join session: %w", err)
	}
	defer conn.Close()

	tlsConn := tls.Client(conn, &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	})

	if err := tlsConn.Handshake(); err != nil {
		slog.Error("TLS handshake failed", "error", err)
		return fmt.Errorf("TLS handshake failed: %w", err)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		tlsConn.Close()
		return fmt.Errorf("server presented no TLS certificates")
	}

	peerID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
	if peerID != serverID {
		tlsConn.Close()
		return fmt.Errorf("security mismatch: connected to device %s, expected %s", peerID, serverID)
	}

	slog.Info("Connected successfully via relay", "peerID", peerID.String())
	
	if !tryDirect {
		slog.Info("Relay-only mode requested. Bypassing ICE.")
		handleClientConn(ctx, tlsConn, localSocks, isShell)
		return nil
	}

	slog.Info("Starting ICE/WebRTC negotiation via relay signaling...")
	p2pConn, err := negotiateWebRTCClient(ctx, tlsConn)
	if err != nil {
		slog.Warn("ICE negotiation failed, falling back to relay", "error", err)
		sendSignal(tlsConn, SignalMessage{Type: "fallback"})
		handleClientConn(ctx, tlsConn, localSocks, isShell)
		return nil
	}

	slog.Info("Direct P2P ICE connection established! Closing relay.")
	tlsConn.Close()
	handleClientConn(ctx, p2pConn, localSocks, isShell)
	return nil
}

func handleClientConn(ctx context.Context, finalConn net.Conn, localSocks string, isShell bool) {
	if localSocks != "" {
		runSocksClient(ctx, finalConn, localSocks)
	} else if isShell {
		runShellClient(ctx, finalConn)
	} else {
		defer finalConn.Close()
		pipeBiDirectional(finalConn)
	}
}

func pipeBiDirectional(conn net.Conn) {
	errChan := make(chan error, 2)

	go func() {
		_, err := CopyWithTrace(os.Stdout, conn, "remote->stdout")
		errChan <- err
	}()

	go func() {
		_, err := CopyWithTrace(conn, os.Stdin, "stdin->remote")
		errChan <- err
	}()

	err := <-errChan
	if err != nil && err != io.EOF {
		slog.Error("Connection error", "error", err)
	}
}

func handleForwardConn(conn net.Conn, cert tls.Certificate, forwardAddr string, isRelay bool) {
	defer conn.Close()

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		slog.Error("TLS handshake failed", "error", err)
		return
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		clientID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
		slog.Info("Connection established with client, forwarding", "id", clientID.String(), "target", forwardAddr)
	} else {
		slog.Info("Connection established with anonymous client, forwarding", "target", forwardAddr)
	}

	var dataConn net.Conn
	if isRelay {
		p2pConn, fallback, err := negotiateWebRTCServer(context.Background(), tlsConn)
		if fallback || err != nil {
			slog.Info("Using relay connection for data (ICE bypassed or failed)", "error", err)
			dataConn = tlsConn
		} else {
			slog.Info("Direct P2P ICE connection established! Closing relay.")
			tlsConn.Close()
			dataConn = p2pConn
		}
	} else {
		dataConn = tlsConn
	}

	localConn, err := net.Dial("tcp", forwardAddr)
	if err != nil {
		slog.Error("Failed to connect to forward target", "target", forwardAddr, "error", err)
		if dataConn != tlsConn {
			dataConn.Close()
		} else {
			tlsConn.Close()
		}
		return
	}
	defer localConn.Close()

	errChan := make(chan error, 2)
	go func() {
		_, err := CopyWithTrace(localConn, dataConn, "remote->local")
		errChan <- err
	}()
	go func() {
		_, err := CopyWithTrace(dataConn, localConn, "local->remote")
		errChan <- err
	}()

	err = <-errChan
	if err != nil && err != io.EOF {
		slog.Error("Forward connection closed with error", "error", err)
	}
	if dataConn != tlsConn {
		dataConn.Close()
	} else {
		tlsConn.Close()
	}
}



func systemdNotify(state string) {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return
	}
	conn, err := net.Dial("unixgram", socketPath)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(state))
}
