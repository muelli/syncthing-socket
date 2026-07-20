package main

import (
	"context"
	"io"
	"log/slog"
	"net"

	socks5 "github.com/armon/go-socks5"
	"github.com/hashicorp/yamux"
)

func pipeConns(conn1, conn2 net.Conn) {
	errChan := make(chan error, 2)
	go func() {
		_, err := copyWithTrace(conn1, conn2, "stream->local")
		errChan <- err
	}()
	go func() {
		_, err := copyWithTrace(conn2, conn1, "local->stream")
		errChan <- err
	}()
	err := <-errChan
	if err != nil && err != io.EOF {
		slog.Debug("Multiplexed stream closed", "error", err)
	}
}

func runSocksServer(conn net.Conn) {
	slog.Info("Starting yamux multiplexer and SOCKS5 server")
	session, err := yamux.Server(conn, nil)
	if err != nil {
		slog.Error("Failed to start yamux server", "error", err)
		return
	}
	defer session.Close()

	conf := &socks5.Config{}
	server, err := socks5.New(conf)
	if err != nil {
		slog.Error("Failed to create SOCKS5 server", "error", err)
		return
	}

	for {
		stream, err := session.Accept()
		if err != nil {
			slog.Error("yamux accept error", "error", err)
			return
		}
		go server.ServeConn(stream)
	}
}

func runSocksClient(ctx context.Context, p2pConn net.Conn, localPort string) {
	slog.Info("Starting yamux multiplexer on client")
	session, err := yamux.Client(p2pConn, nil)
	if err != nil {
		slog.Error("Failed to start yamux client", "error", err)
		return
	}
	defer session.Close()

	listener, err := net.Listen("tcp", localPort)
	if err != nil {
		slog.Error("Failed to start local SOCKS proxy", "error", err, "port", localPort)
		return
	}
	defer listener.Close()

	slog.Info("Local SOCKS5 proxy running", "address", localPort)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		localConn, err := listener.Accept()
		if err != nil {
			return
		}

		go func(lc net.Conn) {
			defer lc.Close()
			remoteStream, err := session.Open()
			if err != nil {
				slog.Error("Failed to open yamux stream", "error", err)
				return
			}
			defer remoteStream.Close()

			pipeConns(lc, remoteStream)
		}(localConn)
	}
}
