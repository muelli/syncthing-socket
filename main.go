package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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

func main() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	serverCert := serverCmd.String("cert", "", "Path to TLS certificate (optional)")
	serverKey := serverCmd.String("key", "", "Path to TLS key (optional)")
	serverRelay := serverCmd.String("relay", "dynamic+https://relays.syncthing.net/endpoint", "Relay URI or dynamic pool URL")
	serverDiscovery := serverCmd.String("discovery", "https://discovery-announce-v4.syncthing.net/v2/?nolookup,https://discovery-announce-v6.syncthing.net/v2/?nolookup", "Comma-separated discovery announce URLs")
	serverForward := serverCmd.String("forward", "", "Forward incoming connections to this host:port (e.g. 127.0.0.1:22)")

	clientCmd := flag.NewFlagSet("client", flag.ExitOnError)
	clientCert := clientCmd.String("cert", "", "Path to TLS certificate (optional)")
	clientKey := clientCmd.String("key", "", "Path to TLS key (optional)")
	clientRelay := clientCmd.String("relay", "", "Relay URI (if specified, bypasses discovery lookup)")
	clientDiscovery := clientCmd.String("discovery", "https://discovery-lookup.syncthing.net/v2/", "Discovery lookup URL")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch os.Args[1] {
	case "server":
		serverCmd.Parse(os.Args[2:])
		var cert tls.Certificate
		var err error
		if *serverCert != "" && *serverKey != "" {
			cert, err = loadOrGenerateCert(*serverCert, *serverKey)
		} else if *serverCert == "" && *serverKey == "" {
			cert, err = tlsutil.NewCertificateInMemory("syncthing-socket-server", 365)
		} else {
			log.Fatalf("Error: both -cert and -key must be specified if one is provided")
		}
		if err != nil {
			log.Fatalf("Error getting server cert: %v", err)
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
		if err := runServer(ctx, cert, *serverRelay, discoveryServers, *serverForward); err != nil {
			log.Fatalf("Server error: %v", err)
		}

	case "client":
		clientCmd.Parse(os.Args[2:])
		args := clientCmd.Args()
		if len(args) < 1 {
			fmt.Println("Error: client mode requires target server Device ID")
			clientCmd.Usage()
			os.Exit(1)
		}

		// Support combined string like SERVER_ID@RELAY_URI
		targetStr := args[0]
		var serverID string
		var relayURI string

		if strings.Contains(targetStr, "@") {
			parts := strings.SplitN(targetStr, "@", 2)
			serverID = parts[0]
			relayURI = parts[1]
		} else {
			serverID = targetStr
			relayURI = *clientRelay
		}

		var cert tls.Certificate
		var err error
		if *clientCert != "" && *clientKey != "" {
			cert, err = loadOrGenerateCert(*clientCert, *clientKey)
		} else if *clientCert == "" && *clientKey == "" {
			cert, err = tlsutil.NewCertificateInMemory("syncthing-socket-client", 365)
		} else {
			log.Fatalf("Error: both -cert and -key must be specified if one is provided")
		}
		if err != nil {
			log.Fatalf("Error getting client cert: %v", err)
		}

		if err := runClient(ctx, serverID, relayURI, cert, *clientDiscovery); err != nil {
			log.Fatalf("Client error: %v", err)
		}

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
	fmt.Println("Run 'syncthing-socket <command> -h' for options.")
}

func loadOrGenerateCert(certPath, keyPath string) (tls.Certificate, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}
	log.Printf("Generating new TLS certificate in %s and %s...", certPath, keyPath)
	return tlsutil.NewCertificate(certPath, keyPath, "syncthing-socket", 3650)
}

func announce(ctx context.Context, cert tls.Certificate, relayURI string, discoveryServers []string) {
	type AnnouncePayload struct {
		Addresses []string `json:"addresses"`
	}
	payload := AnnouncePayload{
		Addresses: []string{relayURI},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal announce payload: %v", err)
		return
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true, // handle self-signed certs on discovery servers
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
				log.Printf("Announcing availability to %s...", ds)
				req, err := http.NewRequestWithContext(ctx, "POST", ds, strings.NewReader(string(body)))
				if err != nil {
					log.Printf("Announce: failed to create request for %s: %v", ds, err)
				} else {
					req.Header.Set("Content-Type", "application/json")
					resp, err := client.Do(req)
					if err != nil {
						log.Printf("Announce to %s failed: %v", ds, err)
					} else {
						resp.Body.Close()
						if resp.StatusCode == http.StatusNoContent {
							log.Printf("Successfully announced to %s", ds)
						} else {
							log.Printf("Announce to %s returned status %d", ds, resp.StatusCode)
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

func lookup(ctx context.Context, serverID string, discoveryServer string) (string, error) {
	urlStr := fmt.Sprintf("%s&device=%s", discoveryServer, serverID)
	if !strings.Contains(discoveryServer, "?") {
		urlStr = fmt.Sprintf("%s?device=%s", discoveryServer, serverID)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // safe since we verify the server's identity end-to-end
		},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("device %s not found in discovery", serverID)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery lookup returned HTTP %d", resp.StatusCode)
	}

	type LookupResponse struct {
		Addresses []string `json:"addresses"`
	}
	var lr LookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", err
	}

	for _, addr := range lr.Addresses {
		if strings.HasPrefix(addr, "relay://") {
			return addr, nil
		}
	}

	return "", fmt.Errorf("no relay addresses found for device %s (found: %v)", serverID, lr.Addresses)
}

func runServer(ctx context.Context, cert tls.Certificate, relayURI string, discoveryServers []string, forwardAddr string) error {
	u, err := url.Parse(relayURI)
	if err != nil {
		return fmt.Errorf("invalid relay URI: %w", err)
	}

	serverID := syncthingprotocol.NewDeviceID(cert.Certificate[0])
	fmt.Println("==================================================")
	fmt.Printf("Server Device ID: %s\n", serverID)
	fmt.Println("==================================================")

	relayClient, err := client.NewClient(u, []tls.Certificate{cert}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("failed to create relay client: %w", err)
	}

	go func() {
		if err := relayClient.Serve(ctx); err != nil {
			log.Printf("Relay client stopped: %v", err)
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

	fmt.Printf("Connected to Relay: %s\n", connectedURI.String())
	fmt.Printf("To connect, run:\n")
	fmt.Printf("  ./syncthing-socket client -relay %q %s\n", connectedURI.String(), serverID.String())
	fmt.Printf("Or simply:\n")
	fmt.Printf("  ./syncthing-socket client %s@%s\n", serverID.String(), connectedURI.String())
	fmt.Println("==================================================")

	// Start background announcement
	if len(discoveryServers) > 0 {
		announce(ctx, cert, connectedURI.String(), discoveryServers)
	}

	invs := relayClient.Invitations()
	for {
		select {
		case inv, ok := <-invs:
			if !ok {
				return fmt.Errorf("invitations channel closed")
			}
			log.Printf("Received invitation from relay. Joining session...")
			conn, err := client.JoinSession(ctx, inv)
			if err != nil {
				log.Printf("Failed to join session: %v", err)
				continue
			}

			if forwardAddr != "" {
				go handleForwardConn(conn, cert, forwardAddr)
			} else {
				handleServerConn(conn, cert)
				return nil // exit after the first connection closes (netcat style)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func handleServerConn(conn net.Conn, cert tls.Certificate) {
	defer conn.Close()

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake failed: %v", err)
		return
	}
	defer tlsConn.Close()

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		clientID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
		log.Printf("Connection established with client: %s", clientID.String())
	} else {
		log.Printf("Connection established with anonymous client")
	}

	pipeBiDirectional(tlsConn)
}

func runClient(ctx context.Context, serverIDStr string, relayURIOverride string, cert tls.Certificate, discoveryServer string) error {
	serverID, err := syncthingprotocol.DeviceIDFromString(serverIDStr)
	if err != nil {
		return fmt.Errorf("invalid server Device ID: %w", err)
	}

	var relayURI string
	if relayURIOverride != "" {
		relayURI = relayURIOverride
	} else {
		fmt.Println("Looking up server address on discovery server...")
		resolved, err := lookup(ctx, serverID.String(), discoveryServer)
		if err != nil {
			return fmt.Errorf("discovery lookup failed: %w", err)
		}
		relayURI = resolved
		fmt.Printf("Resolved server address: %s\n", relayURI)
	}

	u, err := url.Parse(relayURI)
	if err != nil {
		return fmt.Errorf("invalid relay URI: %w", err)
	}

	log.Printf("Requesting session invitation from relay...")
	invitation, err := client.GetInvitationFromRelay(ctx, u, serverID, []tls.Certificate{cert}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("failed to get invitation from relay: %w", err)
	}

	log.Printf("Joining relay session...")
	conn, err := client.JoinSession(ctx, invitation)
	if err != nil {
		return fmt.Errorf("failed to join session: %w", err)
	}
	defer conn.Close()

	tlsConn := tls.Client(conn, &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // Manual verification using Device ID below
		MinVersion:         tls.VersionTLS13,
	})

	if err := tlsConn.Handshake(); err != nil {
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	defer tlsConn.Close()

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return fmt.Errorf("server presented no TLS certificates")
	}

	peerID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
	if peerID != serverID {
		return fmt.Errorf("security mismatch: connected to device %s, expected %s", peerID, serverID)
	}

	log.Printf("Connected successfully. Communication is now active:")
	pipeBiDirectional(tlsConn)
	return nil
}

func pipeBiDirectional(conn net.Conn) {
	errChan := make(chan error, 2)

	go func() {
		_, err := io.Copy(os.Stdout, conn)
		errChan <- err
	}()

	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errChan <- err
	}()

	err := <-errChan
	if err != nil && err != io.EOF {
		log.Printf("Connection error: %v", err)
	}
}

func handleForwardConn(conn net.Conn, cert tls.Certificate, forwardAddr string) {
	defer conn.Close()

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake failed: %v", err)
		return
	}
	defer tlsConn.Close()

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		clientID := syncthingprotocol.NewDeviceID(state.PeerCertificates[0].Raw)
		log.Printf("Connection established with client: %s -> forwarding to %s", clientID.String(), forwardAddr)
	} else {
		log.Printf("Connection established with anonymous client -> forwarding to %s", forwardAddr)
	}

	localConn, err := net.Dial("tcp", forwardAddr)
	if err != nil {
		log.Printf("Failed to connect to forward target %s: %v", forwardAddr, err)
		return
	}
	defer localConn.Close()

	errChan := make(chan error, 2)
	go func() {
		_, err := io.Copy(localConn, tlsConn)
		errChan <- err
	}()
	go func() {
		_, err := io.Copy(tlsConn, localConn)
		errChan <- err
	}()

	err = <-errChan
	if err != nil && err != io.EOF {
		log.Printf("Forward connection closed with error: %v", err)
	}
}
