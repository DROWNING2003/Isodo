package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	t3NodeBinary = "/usr/local/bin/node"
	t3CLIPath    = "/opt/isodo/t3/bin.mjs"
	t3BaseDir    = "/workspace/.t3"
)

// CreateSandboxAppLink starts a loopback relay to the Guest T3 server and
// returns a short-lived pairing URL. The returned URL is Node-local on purpose:
// the Control/Preview Gateway must publish it; clients never dial the Node.
func (s *CHVServer) CreateSandboxAppLink(ctx context.Context, request *nodev1.CreateSandboxAppLinkRequest) (*nodev1.SandboxAppLink, error) {
	if request == nil || request.GetSandboxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID is required")
	}
	if !canonicalUUID(request.GetSandboxId()) {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID must be a canonical UUID")
	}
	s.mu.Lock()
	record, ok := s.journal.getSandbox(request.GetSandboxId())
	instance := s.live[request.GetSandboxId()]
	s.mu.Unlock()
	if !ok || (request.GetOwnershipGeneration() != 0 && record.Generation != request.GetOwnershipGeneration()) {
		return nil, status.Error(codes.NotFound, "Sandbox ownership was not found on this Node")
	}
	if record.State != "running" || instance == nil {
		return nil, status.Error(codes.FailedPrecondition, "Sandbox Guest is not running")
	}
	identity := instance.Identity()
	if identity == nil || identity.GetSandboxId() != record.SandboxID || identity.GetOwnershipGeneration() != record.Generation || identity.GetBootId() == "" || identity.GetGuestEpoch() == "" {
		return nil, status.Error(codes.FailedPrecondition, "Sandbox Guest identity is not ready")
	}
	if err := s.prepareCHVEgress(ctx, record, instance); err != nil {
		return nil, status.Error(codes.Unavailable, "Sandbox egress proxy could not be configured")
	}
	socket := instance.VsockSocket()
	if socket == "" {
		return nil, status.Error(codes.FailedPrecondition, "Sandbox runtime does not expose a Guest socket")
	}
	relay, err := startAppRelay(s.ctx, func(dialCtx context.Context, port uint16) (net.Conn, error) {
		return s.dialGuestPort(dialCtx, socket, identity, port)
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "start Sandbox app relay: %v", err)
	}
	established := false
	defer func() {
		if !established {
			relay.Close()
		}
	}()
	if err := waitT3Ready(ctx, relay.URL()); err != nil {
		return nil, status.Errorf(codes.Unavailable, "T3 application is not ready: %v", err)
	}

	connection, err := guesttransport.NewVsockClientConn(socket)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "connect to Sandbox Guest: %v", err)
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)
	output, err := runT3GuestCommandStdout(ctx, client, identity, "t3-pair-"+newT3RequestSuffix(), []string{
		t3NodeBinary, t3CLIPath, "auth", "pairing", "create",
		"--base-dir", t3BaseDir, "--ttl", "5m", "--json",
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "create T3 pairing credential: %v", err)
	}
	var issued struct {
		Credential string `json:"credential"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(output), &issued); err != nil || issued.Credential == "" || issued.ExpiresAt == "" {
		return nil, status.Error(codes.Internal, "T3 pairing CLI returned an invalid credential response")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, issued.ExpiresAt)
	if err != nil {
		return nil, status.Error(codes.Internal, "T3 pairing CLI returned an invalid expiry time")
	}
	if !expiresAt.After(time.Now()) {
		return nil, status.Error(codes.Internal, "T3 pairing CLI returned an expired credential")
	}
	_, relayPort, err := net.SplitHostPort(relay.Addr())
	if err != nil {
		return nil, status.Error(codes.Internal, "Sandbox application relay address is invalid")
	}
	appURL, err := url.Parse(fmt.Sprintf("http://%d-%s.localhost:%s", t3GuestPort, record.SandboxID, relayPort))
	if err != nil {
		return nil, status.Error(codes.Internal, "Sandbox application URL is invalid")
	}
	appURL.Path = "/pair"
	appURL.RawQuery = ""
	appURL.Fragment = url.Values{"token": []string{issued.Credential}}.Encode()

	s.mu.Lock()
	if old := s.appLinks[record.SandboxID]; old != nil {
		old.Close()
	}
	s.appLinks[record.SandboxID] = relay
	s.mu.Unlock()
	established = true
	return &nodev1.SandboxAppLink{Url: appURL.String(), ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (s *CHVServer) dialGuestPort(parent context.Context, socket string, identity *guestv1.RuntimeIdentity, port uint16) (net.Conn, error) {
	connection, err := guesttransport.NewVsockClientConn(socket)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stream, err := guestv1.NewGuestServiceClient(connection).PortForward(ctx)
	if err != nil {
		cancel()
		_ = connection.Close()
		return nil, err
	}
	if err := stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_Open{Open: &guestv1.PortForwardOpen{
		Identity: identity, RequestId: "app-" + newT3RequestSuffix(), Port: uint32(port), Service: "t3",
	}}}); err != nil {
		cancel()
		_ = connection.Close()
		return nil, err
	}
	return &portForwardConn{stream: stream, grpcConn: connection, cancel: cancel}, nil
}

func (s *CHVServer) closeAppLinkLocked(sandboxID string) {
	s.closeEgressLocked(sandboxID)
	relay := s.appLinks[sandboxID]
	if relay == nil {
		return
	}
	delete(s.appLinks, sandboxID)
	relay.Close()
}

func waitT3Ready(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
		if err != nil {
			return err
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("T3 endpoint returned status %d", response.StatusCode)
		} else {
			lastErr = requestErr
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for T3 application: %w (last error: %v)", ctx.Err(), lastErr)
		case <-deadline.C:
			return fmt.Errorf("T3 application did not become ready (last error: %v)", lastErr)
		case <-ticker.C:
		}
	}
}
