package main

import (
	"context"
	"log/slog"
	"net"

	"github.com/hashicorp/yamux"
)

func runReverseForwardServer(conn net.Conn, bindAddr string) {
	slog.Info("Starting yamux multiplexer for reverse forwarding")
	session, err := yamux.Server(conn, nil)
	if err != nil {
		slog.Error("Failed to initialize yamux server", "error", err)
		return
	}
	defer session.Close()

	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		slog.Error("Failed to start reverse forward listener", "bindAddr", bindAddr, "error", err)
		return
	}
	defer listener.Close()

	slog.Info("Reverse port forwarding listener started", "bindAddr", bindAddr)

	for {
		localConn, err := listener.Accept()
		if err != nil {
			slog.Error("Reverse forward listener accept failed", "error", err)
			return
		}

		slog.Info("Accepted incoming connection for reverse forwarding", "remote", localConn.RemoteAddr().String())

		go func(localConn net.Conn) {
			defer localConn.Close()

			stream, err := session.OpenStream()
			if err != nil {
				slog.Error("Failed to open yamux stream for reverse forwarding", "error", err)
				return
			}
			defer stream.Close()

			errChan := make(chan error, 2)
			go func() {
				_, err := CopyWithTrace(stream, localConn, "local->remote")
				errChan <- err
			}()
			go func() {
				_, err := CopyWithTrace(localConn, stream, "remote->local")
				errChan <- err
			}()

			<-errChan
		}(localConn)
	}
}

func runReverseForwardClient(ctx context.Context, conn net.Conn, targetAddr string) {
	slog.Info("Starting yamux multiplexer for reverse forwarding on client")
	session, err := yamux.Client(conn, nil)
	if err != nil {
		slog.Error("Failed to initialize yamux client", "error", err)
		return
	}
	defer session.Close()

	slog.Info("Ready to accept reverse-forwarded streams", "targetAddr", targetAddr)

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			slog.Error("Failed to accept yamux stream", "error", err)
			return
		}

		slog.Info("Accepted new reverse-forwarded stream, dialing target", "targetAddr", targetAddr)

		go func(stream net.Conn) {
			defer stream.Close()

			targetConn, err := net.Dial("tcp", targetAddr)
			if err != nil {
				slog.Error("Failed to dial reverse forward target", "targetAddr", targetAddr, "error", err)
				return
			}
			defer targetConn.Close()

			errChan := make(chan error, 2)
			go func() {
				_, err := CopyWithTrace(targetConn, stream, "remote->target")
				errChan <- err
			}()
			go func() {
				_, err := CopyWithTrace(stream, targetConn, "target->remote")
				errChan <- err
			}()

			<-errChan
		}(stream)
	}
}
