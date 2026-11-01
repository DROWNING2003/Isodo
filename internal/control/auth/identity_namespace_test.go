package auth

import (
	"context"
	"testing"
)

func TestIdentityNamespaceKeepsTenantAcrossIssuerMove(t *testing.T) {
	s := &namespaceStoreStub{memoryWebStore: newMemoryWebStore()}
	wrapped := identityNamespace{WebStore: s, verifiedIssuer: "https://auth.drowning.top:18443", namespace: "http://10.210.10.32:18000"}
	id, err := wrapped.ResolveTenant(t.Context(), "https://auth.drowning.top:18443", "stable-subject", "Alice")
	if err != nil || id != "tenant-existing" || s.issuer != "http://10.210.10.32:18000" {
		t.Fatal("identity namespace was not preserved")
	}
	if _, err := wrapped.ResolveTenant(t.Context(), "https://evil.example", "stable-subject", "Alice"); err == nil {
		t.Fatal("foreign issuer accepted")
	}
}

type namespaceStoreStub struct {
	*memoryWebStore
	issuer string
}

func (s *namespaceStoreStub) ResolveTenant(_ context.Context, issuer, _, _ string) (string, error) {
	s.issuer = issuer
	return "tenant-existing", nil
}
