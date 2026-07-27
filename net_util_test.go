package main

import "testing"

func TestParseNetworkAndAddr(t *testing.T) {
	tests := []struct {
		input    string
		wantNet  string
		wantAddr string
	}{
		{"127.0.0.1:8080", "tcp", "127.0.0.1:8080"},
		{"tcp://localhost:22", "tcp", "localhost:22"},
		{"tcp:0.0.0.0:443", "tcp", "0.0.0.0:443"},
		{"/var/run/docker.sock", "unix", "/var/run/docker.sock"},
		{"unix:///tmp/test.sock", "unix", "/tmp/test.sock"},
		{"unix:./app.socket", "unix", "./app.socket"},
		{"./local.sock", "unix", "./local.sock"},
		{"mysocket.sock", "unix", "mysocket.sock"},
		{"socket_no_colon", "unix", "socket_no_colon"},
	}
	for _, tc := range tests {
		net, addr := parseNetworkAndAddr(tc.input)
		if net != tc.wantNet || addr != tc.wantAddr {
			t.Errorf("parseNetworkAndAddr(%q) = (%q, %q), want (%q, %q)", tc.input, net, addr, tc.wantNet, tc.wantAddr)
		}
	}
}
