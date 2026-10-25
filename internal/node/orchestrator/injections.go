package orchestrator

import (
	"context"
	"errors"

	"github.com/DROWNING2003/Isodo/internal/node/injection"
)

// EnableInjections attaches the host-side secure-connection proxy manager. The
// Lima backend uses it to inject headers for local sandboxes; the CHV backend
// leaves it nil.
func (s *LimaServer) EnableInjections(manager *injection.Manager) {
	s.injections = manager
}

// SandboxInjections returns the masked rule set for one local sandbox.
func (s *LimaServer) SandboxInjections(sandboxID string) injection.SandboxConfig {
	if s.injections == nil {
		return injection.SandboxConfig{}
	}
	return s.injections.Get(sandboxID)
}

// SetSandboxInjections persists rules and updates the running proxy in place.
func (s *LimaServer) SetSandboxInjections(_ context.Context, sandboxID string, rules []injection.Rule) (injection.SandboxConfig, error) {
	if s.injections == nil {
		return injection.SandboxConfig{}, errors.New("secure connections are not enabled on this Node")
	}
	return s.injections.SetRules(sandboxID, rules)
}

func (s *LimaServer) DeleteSandboxInjections(sandboxID string) error {
	if s.injections == nil {
		return nil
	}
	return s.injections.Delete(sandboxID)
}

// RemovePersistentInjection revokes one account rule from every local sandbox.
func (s *LimaServer) RemovePersistentInjection(persistentID string) error {
	if s.injections == nil || persistentID == "" {
		return nil
	}
	return s.injections.RemovePersistentRule(persistentID)
}

func (s *LimaServer) InjectionSummary() map[string]int {
	if s.injections == nil {
		return map[string]int{}
	}
	return s.injections.Summary()
}

func (s *LimaServer) InjectionCACertificate() []byte {
	if s.injections == nil {
		return nil
	}
	return s.injections.CACertPEM()
}

func (s *LimaServer) InjectionProxyPort(sandboxID string) int {
	if s.injections == nil {
		return 0
	}
	return s.injections.ProxyPort(sandboxID)
}

func (s *LimaServer) InjectionStats(sandboxID string) injection.Stats {
	if s.injections == nil {
		return injection.Stats{}
	}
	return s.injections.Stats(sandboxID)
}
