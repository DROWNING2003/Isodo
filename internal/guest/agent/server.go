package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	ProtocolMajor = 1
	ProtocolMinor = 0
	VsockPort     = 1024
)

type Identity struct {
	SandboxID           string
	OwnershipGeneration uint64
	BootID              string
	GuestEpoch          string
}

type Server struct {
	guestv1.UnimplementedGuestServiceServer
	mu                 sync.RWMutex
	processMu          sync.Mutex
	identity           Identity
	workspace          string
	processes          map[string]*guestProcess
	processRequests    map[string]processRequestRecord
	bound              bool
	boundByBindRuntime bool
}

func NewServer(identity Identity) *Server {
	return &Server{identity: identity, workspace: os.Getenv("ISODO_WORKSPACE"), bound: identity.complete()}
}

func IdentityFromEnvironment() (Identity, error) {
	templateMode, err := strconv.ParseBool(os.Getenv("ISODO_TEMPLATE_MODE"))
	if err != nil && os.Getenv("ISODO_TEMPLATE_MODE") != "" {
		return Identity{}, errors.New("ISODO_TEMPLATE_MODE must be true or false")
	}
	if templateMode {
		if os.Getenv("ISODO_SANDBOX_ID") != "" || os.Getenv("ISODO_OWNERSHIP_GENERATION") != "" || os.Getenv("ISODO_BOOT_ID") != "" || os.Getenv("ISODO_GUEST_EPOCH") != "" {
			return Identity{}, errors.New("template mode cannot include a runtime identity")
		}
		return Identity{}, nil
	}
	generation, err := strconv.ParseUint(os.Getenv("ISODO_OWNERSHIP_GENERATION"), 10, 64)
	if err != nil || generation == 0 {
		return Identity{}, errors.New("ISODO_OWNERSHIP_GENERATION must be a positive integer")
	}
	identity := Identity{
		SandboxID:           strings.TrimSpace(os.Getenv("ISODO_SANDBOX_ID")),
		OwnershipGeneration: generation,
		BootID:              strings.TrimSpace(os.Getenv("ISODO_BOOT_ID")),
		GuestEpoch:          strings.TrimSpace(os.Getenv("ISODO_GUEST_EPOCH")),
	}
	if identity.SandboxID == "" || identity.BootID == "" || identity.GuestEpoch == "" {
		return Identity{}, errors.New("ISODO_SANDBOX_ID, ISODO_BOOT_ID, and ISODO_GUEST_EPOCH are required")
	}
	return identity, nil
}

func (s *Server) Ready(ctx context.Context, _ *emptypb.Empty) (*guestv1.ReadyResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	identity, bound := s.identity, s.bound
	s.mu.RUnlock()
	var runtimeIdentity *guestv1.RuntimeIdentity
	if bound {
		runtimeIdentity = &guestv1.RuntimeIdentity{
			SandboxId:           identity.SandboxID,
			OwnershipGeneration: identity.OwnershipGeneration,
			BootId:              identity.BootID,
			GuestEpoch:          identity.GuestEpoch,
		}
	}
	return &guestv1.ReadyResponse{
		ProtocolMajor: ProtocolMajor,
		ProtocolMinor: ProtocolMinor,
		Identity:      runtimeIdentity,
		AgentVersion:  "0.1.0-dev",
		Capabilities:  []string{"ready.v1"},
		Limits: &guestv1.GuestLimits{
			MaxGrpcMessageBytes: 1 << 20,
		},
		RuntimeBound: bound,
	}, nil
}

func (s *Server) BindRuntime(ctx context.Context, request *guestv1.BindRuntimeRequest) (*guestv1.ReadyResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || strings.TrimSpace(request.GetSandboxId()) == "" || request.GetOwnershipGeneration() == 0 || strings.TrimSpace(request.GetBootId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID, positive ownership generation, and boot ID are required")
	}
	if len(request.GetSandboxId()) > 256 || len(request.GetBootId()) > 256 {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID and boot ID must not exceed 256 bytes")
	}
	sandboxID := strings.TrimSpace(request.GetSandboxId())
	bootID := strings.TrimSpace(request.GetBootId())

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound {
		if s.boundByBindRuntime && s.identity.SandboxID == sandboxID && s.identity.OwnershipGeneration == request.GetOwnershipGeneration() && s.identity.BootID == bootID {
			return s.readyResponseLocked(), nil
		}
		return nil, status.Error(codes.FailedPrecondition, "guest is already bound to another runtime identity")
	}
	epoch := make([]byte, 16)
	if _, err := rand.Read(epoch); err != nil {
		return nil, status.Errorf(codes.Internal, "generate guest epoch: %v", err)
	}
	s.identity = Identity{
		SandboxID:           sandboxID,
		OwnershipGeneration: request.GetOwnershipGeneration(),
		BootID:              bootID,
		GuestEpoch:          hex.EncodeToString(epoch),
	}
	s.bound = true
	s.boundByBindRuntime = true
	return s.readyResponseLocked(), nil
}

// IsRuntimeBound reports whether this Guest Agent has been assigned to one Sandbox.
func (s *Server) IsRuntimeBound() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bound
}

func (s *Server) checkIdentity(request *guestv1.RuntimeIdentity) error {
	s.mu.RLock()
	identity, bound := s.identity, s.bound
	s.mu.RUnlock()
	if !bound {
		return status.Error(codes.FailedPrecondition, "guest runtime identity is not bound")
	}
	if request == nil || request.GetSandboxId() != identity.SandboxID || request.GetOwnershipGeneration() != identity.OwnershipGeneration || request.GetBootId() != identity.BootID || request.GetGuestEpoch() != identity.GuestEpoch {
		return status.Error(codes.FailedPrecondition, "STALE_RUNTIME: request identity does not match this Guest")
	}
	return nil
}

// UnaryInterceptor prevents workload RPCs from reaching an unbound template Guest.
func (s *Server) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !s.IsRuntimeBound() && !isBindingMethod(info.FullMethod) {
			return nil, status.Error(codes.FailedPrecondition, "guest runtime identity is not bound")
		}
		return handler(ctx, request)
	}
}

// StreamInterceptor prevents workload streams from reaching an unbound template Guest.
func (s *Server) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !s.IsRuntimeBound() && !isBindingMethod(info.FullMethod) {
			return status.Error(codes.FailedPrecondition, "guest runtime identity is not bound")
		}
		return handler(service, stream)
	}
}

func (s *Server) readyResponseLocked() *guestv1.ReadyResponse {
	identity := s.identity
	return &guestv1.ReadyResponse{
		ProtocolMajor: ProtocolMajor,
		ProtocolMinor: ProtocolMinor,
		Identity: &guestv1.RuntimeIdentity{
			SandboxId:           identity.SandboxID,
			OwnershipGeneration: identity.OwnershipGeneration,
			BootId:              identity.BootID,
			GuestEpoch:          identity.GuestEpoch,
		},
		AgentVersion: "0.1.0-dev",
		Capabilities: []string{"ready.v1"},
		Limits:       &guestv1.GuestLimits{MaxGrpcMessageBytes: 1 << 20},
		RuntimeBound: true,
	}
}

func isBindingMethod(fullMethod string) bool {
	return strings.HasSuffix(fullMethod, "/Ready") || strings.HasSuffix(fullMethod, "/BindRuntime")
}

func (identity Identity) complete() bool {
	return identity.SandboxID != "" && identity.OwnershipGeneration > 0 && identity.BootID != "" && identity.GuestEpoch != ""
}
