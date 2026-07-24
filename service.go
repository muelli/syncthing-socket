package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"

	"github.com/kardianos/service"
	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

type serverService struct {
	ctx              context.Context
	cancel           context.CancelFunc
	cert             tls.Certificate
	relayURI         string
	discoveryServers []string
	forwardAddr      string
	directPort       int
	isSocks          bool
	isShell          bool
	isCommand        string
	proxyProtocol    bool
	reverseForward   string
	authorizedClients []syncthingprotocol.DeviceID
}

func (p *serverService) Start(s service.Service) error {
	p.ctx, p.cancel = context.WithCancel(context.Background())
	go func() {
		if err := runServer(p.ctx, p.cert, p.relayURI, p.discoveryServers, p.forwardAddr, p.directPort, p.isSocks, p.isShell, p.isCommand, p.proxyProtocol, p.reverseForward, p.authorizedClients); err != nil {
			slog.Error("Server error", "error", err)
		}
	}()
	return nil
}

func (p *serverService) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// runServerWrapper wraps the execution in kardianos/service to support Windows SCM
func runServerWrapper(ctx context.Context, cert tls.Certificate, relayURI string, discoveryServers []string, forwardAddr string, directPort int, isSocks bool, isShell bool, isCommand string, proxyProtocol bool, reverseForward string, authorizedClients []syncthingprotocol.DeviceID) error {
	svcConfig := &service.Config{
		Name:        "syncthing-socket",
		DisplayName: "Syncthing Socket",
		Description: "P2P NAT traversal tunnel",
	}

	prg := &serverService{
		cert:              cert,
		relayURI:          relayURI,
		discoveryServers:  discoveryServers,
		forwardAddr:       forwardAddr,
		directPort:        directPort,
		isSocks:           isSocks,
		isShell:           isShell,
		isCommand:         isCommand,
		proxyProtocol:     proxyProtocol,
		reverseForward:    reverseForward,
		authorizedClients: authorizedClients,
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		return err
	}

	if service.Interactive() {
		// Just run it directly with the provided context
		prg.ctx, prg.cancel = context.WithCancel(ctx)
		defer prg.cancel()
		return runServer(prg.ctx, cert, relayURI, discoveryServers, forwardAddr, directPort, isSocks, isShell, isCommand, proxyProtocol, reverseForward, authorizedClients)
	}

	// Running as a service (e.g. Windows SCM)
	return s.Run()
}

func runInstallService(args []string) error {
	svcConfig := &service.Config{
		Name:        "syncthing-socket",
		DisplayName: "Syncthing Socket",
		Description: "P2P NAT traversal tunnel",
		Arguments:   append([]string{"server"}, args...),
	}

	prg := &serverService{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		return fmt.Errorf("failed to create service configuration: %w", err)
	}

	if err := s.Install(); err != nil {
		return fmt.Errorf("failed to install service (did you run as root/Admin?): %w", err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	fmt.Println("Service installed and started successfully.")
	return nil
}
