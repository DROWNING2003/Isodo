package guesttransport

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/guest/agent"
	"google.golang.org/grpc"
)

func TestWaitReadyTCPUsesLoopbackGuestAgent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(server, agent.NewServer(agent.Identity{
		SandboxID:           "sandbox-test",
		OwnershipGeneration: 7,
		BootID:              "boot-test",
		GuestEpoch:          "epoch-test",
	}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response, err := WaitReadyTCP(ctx, listener.Addr().String(), &guestv1.RuntimeIdentity{
		SandboxId:           "sandbox-test",
		OwnershipGeneration: 7,
		BootId:              "boot-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Identity.GuestEpoch != "epoch-test" {
		t.Fatalf("guest epoch = %q, want epoch-test", response.Identity.GuestEpoch)
	}
}

func TestNewTCPClientConnRejectsNonLoopbackAddress(t *testing.T) {
	if _, err := NewTCPClientConn("192.0.2.10:1024"); err == nil {
		t.Fatal("NewTCPClientConn accepted a non-loopback host address")
	}
}

func TestBindRuntimeTCPBindsTemplateGuest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(server, agent.NewServer(agent.Identity{}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	response, err := BindRuntimeTCP(ctx, listener.Addr().String(), &guestv1.RuntimeIdentity{
		SandboxId:           "sandbox-template",
		OwnershipGeneration: 2,
		BootId:              "boot-template",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.RuntimeBound || response.Identity.SandboxId != "sandbox-template" {
		t.Fatalf("bound identity = %+v, want sandbox-template", response.Identity)
	}
}

func TestLimaGuestReadyBindIntegration(t *testing.T) {
	address := os.Getenv("ISODO_LIMA_GUEST_ADDRESS")
	if address == "" {
		t.Skip("set ISODO_LIMA_GUEST_ADDRESS to a loopback-forwarded Lima Guest Agent")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := WaitTemplateReadyTCP(ctx, address); err != nil {
		t.Fatalf("wait for Lima template Guest: %v", err)
	}
	response, err := BindRuntimeTCP(ctx, address, &guestv1.RuntimeIdentity{
		SandboxId:           "lima-integration-probe",
		OwnershipGeneration: 1,
		BootId:              "lima-integration-boot",
	})
	if err != nil {
		t.Fatalf("bind Lima Guest identity: %v", err)
	}
	if !response.RuntimeBound || response.Identity.GetSandboxId() != "lima-integration-probe" {
		t.Fatalf("Lima Guest returned unexpected bound identity: %+v", response.Identity)
	}
}

func TestDialVsockConsumesConnectAcknowledgement(t *testing.T) {
	socketPath := startHandshakeServer(t, "OK 9001", "guest-data")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	conn, err := dialVsock(ctx, socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data := make([]byte, len("guest-data"))
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "guest-data" {
		t.Fatalf("first guest protocol bytes = %q, want guest-data", data)
	}
}

func TestDialVsockRejectsFailedConnectAcknowledgement(t *testing.T) {
	socketPath := startHandshakeServer(t, "Failed", "")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	conn, err := dialVsock(ctx, socketPath)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dialVsock accepted a failed acknowledgement")
	}
}

func startHandshakeServer(t *testing.T, acknowledgement, payload string) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "iv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || line != "CONNECT 1024\n" {
			return
		}
		if _, err := io.WriteString(conn, acknowledgement+"\n"+payload); err != nil {
			return
		}
		<-t.Context().Done()
	}()
	return socketPath
}

func TestVsockHandshakeDoesNotConsumeGRPCConnectDeadline(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "iv-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := dialVsock(ctx, socket)
	if conn != nil {
		conn.Close()
	}
	select {
	case peer := <-accepted:
		peer.Close()
	default:
	}
	if err == nil {
		t.Fatal("accepted relay without acknowledgement")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("handshake consumed outer connection deadline")
	}
	if ctx.Err() != nil {
		t.Fatal("handshake exhausted caller context")
	}
}

func TestReadyRejectsChangedGuestEpoch(t *testing.T) {
	expected := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "original"}
	response := &guestv1.ReadyResponse{RuntimeBound: true, ProtocolMajor: 1, Identity: &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "restarted"}}
	if err := validateReady(response, expected); err == nil {
		t.Fatal("accepted another Guest process in the same assigned boot")
	}
	response.Identity.GuestEpoch = "original"
	if err := validateReady(response, expected); err != nil {
		t.Fatal(err)
	}
}
