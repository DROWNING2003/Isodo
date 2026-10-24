package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/guest/agent"
	"github.com/mdlayher/vsock"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("guest agent stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	identity, err := agent.IdentityFromEnvironment()
	if err != nil {
		return err
	}
	guestServer := agent.NewServer(identity)
	transport := os.Getenv("ISODO_GUEST_TRANSPORT")
	if transport == "" {
		transport = "vsock"
	}
	var listener net.Listener
	switch transport {
	case "vsock":
		listener, err = vsock.Listen(agent.VsockPort, nil)
		if err != nil {
			return fmt.Errorf("listen on guest vsock port %d: %w", agent.VsockPort, err)
		}
	case "tcp":
		address := os.Getenv("ISODO_GUEST_LISTEN_ADDRESS")
		if address == "" {
			address = "127.0.0.1:1024"
		}
		listener, err = net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("listen on Lima Guest Agent TCP address %q: %w", address, err)
		}
		logger.Info("guest agent listening", "transport", "tcp", "address", address)
	default:
		return fmt.Errorf("unsupported guest transport %q", transport)
	}
	defer listener.Close()

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxSendMsgSize(1<<20),
		grpc.UnaryInterceptor(guestServer.UnaryInterceptor()),
		grpc.StreamInterceptor(guestServer.StreamInterceptor()),
	)
	guestv1.RegisterGuestServiceServer(server, guestServer)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	logger.Info("guest agent ready", "transport", transport)

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	case <-shutdownCtx.Done():
		server.GracefulStop()
		return nil
	}
}
