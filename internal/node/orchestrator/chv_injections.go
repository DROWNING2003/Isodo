package orchestrator

import (
	"context"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"maps"
)

// Caller holds s.mu: configuration cannot race a lifecycle revision change.
func (s *CHVServer) injectionTarget(target *nodev1.SandboxTarget) error {
	if target == nil || !canonicalUUID(target.GetSandboxId()) || target.GetOwnershipGeneration() == 0 || target.GetRevision() == 0 {
		return status.Error(codes.InvalidArgument, "sandbox identity and revision are required")
	}
	record, ok := s.journal.getSandbox(target.GetSandboxId())
	if !ok || record.Generation != target.GetOwnershipGeneration() {
		return status.Error(codes.NotFound, "sandbox ownership not found")
	}
	if record.Revision != target.GetRevision() || s.busy[record.SandboxID] || record.State == "deleted" || s.closed {
		return status.Error(codes.FailedPrecondition, "sandbox lifecycle changed")
	}
	if s.injections == nil {
		return status.Error(codes.FailedPrecondition, "injection proxy is not enabled")
	}
	return nil
}

func (s *CHVServer) GetInjections(_ context.Context, target *nodev1.SandboxTarget) (*nodev1.SandboxInjections, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.injectionTarget(target); err != nil {
		return nil, err
	}
	return injectionResponse(s.injections.Get(target.SandboxId), s.injections.Stats(target.SandboxId)), nil
}
func (s *CHVServer) SetInjections(_ context.Context, request *nodev1.SetInjectionsRequest) (*nodev1.SandboxInjections, error) {
	if request == nil || len(request.GetRules()) > 32 {
		return nil, status.Error(codes.InvalidArgument, "at most 32 injection rules are allowed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.injectionTarget(request.GetTarget()); err != nil {
		return nil, err
	}
	rules := make([]injection.Rule, 0, len(request.Rules))
	for _, input := range request.Rules {
		spec := input.GetSpec()
		secret := input.GetSecret()
		rules = append(rules, injection.Rule{Key: input.GetKey(), Name: input.GetName(), Kind: input.GetKind(), PersistentID: input.GetPersistentId(), Spec: injection.Spec{Type: spec.GetType(), BaseURL: spec.GetBaseUrl(), HeaderName: spec.GetHeaderName(), IfHeaders: maps.Clone(spec.GetIfHeaders()), IfQueries: maps.Clone(spec.GetIfQueries())}, Secret: injection.Secret{APIKey: secret.GetApiKey(), Token: secret.GetToken(), Headers: maps.Clone(secret.GetHeaders())}})
	}
	config, err := s.injections.SetRules(request.Target.SandboxId, rules)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "injection rules could not be applied")
	}
	return injectionResponse(config, s.injections.Stats(request.Target.SandboxId)), nil
}
func injectionResponse(config injection.SandboxConfig, stats injection.Stats) *nodev1.SandboxInjections {
	result := &nodev1.SandboxInjections{Enabled: config.Enabled, Injected: stats.Injected}
	for _, rule := range config.Rules {
		result.Rules = append(result.Rules, &nodev1.InjectionView{Key: rule.Key, Name: rule.Name, Kind: rule.Kind, PersistentId: rule.PersistentID, SecretHint: rule.SecretHint, Spec: &nodev1.InjectionSpec{Type: rule.Spec.Type, BaseUrl: rule.Spec.BaseURL, HeaderName: rule.Spec.HeaderName, IfHeaders: maps.Clone(rule.Spec.IfHeaders), IfQueries: maps.Clone(rule.Spec.IfQueries)}})
	}
	return result
}

func (s *CHVServer) RevokeCredential(_ context.Context, request *nodev1.RevokeCredentialRequest) (*emptypb.Empty, error) {
	if request == nil || !canonicalUUID(request.PersistentId) {
		return nil, status.Error(codes.InvalidArgument, "persistent credential ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.injections == nil || s.closed {
		return nil, status.Error(codes.FailedPrecondition, "injection runtime is unavailable")
	}
	if err := s.injections.RemovePersistentRule(request.PersistentId); err != nil {
		return nil, status.Error(codes.Unavailable, "credential revocation failed")
	}
	return &emptypb.Empty{}, nil
}
