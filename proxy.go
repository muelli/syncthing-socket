package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/proxy"
)

// proxyConn wraps a net.Conn and an io.Reader to ensure any data buffered
// during the HTTP CONNECT response reading phase is not lost.
type proxyConn struct {
	net.Conn
	r io.Reader
}

func (c *proxyConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

type httpProxyDialer struct {
	proxyURL *url.URL
	forward  proxy.Dialer
}

func (d *httpProxyDialer) Dial(network, addr string) (net.Conn, error) {
	conn, err := d.forward.Dial("tcp", d.proxyURL.Host)
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}

	if d.proxyURL.User != nil {
		password, _ := d.proxyURL.User.Password()
		req.SetBasicAuth(d.proxyURL.User.Username(), password)
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("HTTP proxy failed with status: %d", resp.StatusCode)
	}

	return &proxyConn{Conn: conn, r: io.MultiReader(br, conn)}, nil
}

func setupProxyEnvironment() {
	// 1. Register custom HTTP CONNECT dialer
	dialerFunc := func(u *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
		return &httpProxyDialer{proxyURL: u, forward: forward}, nil
	}
	proxy.RegisterDialerType("http", dialerFunc)
	proxy.RegisterDialerType("https", dialerFunc)

	// 2. Map standard proxy vars to all_proxy so Syncthing's dialer picks them up.
	// Syncthing dialer reads all_proxy exactly once via sync.Once, so this MUST run at startup.
	if os.Getenv("all_proxy") == "" && os.Getenv("ALL_PROXY") == "" {
		envs := []string{"SOCKS_PROXY", "socks_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"}
		for _, env := range envs {
			if p := os.Getenv(env); p != "" {
				if !strings.Contains(p, "://") {
					p = "http://" + p
				}
				os.Setenv("all_proxy", p)
				break
			}
		}
	}
}
