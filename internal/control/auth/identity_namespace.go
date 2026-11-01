package auth

import (
	"context"
	"errors"
)

// WithIdentityNamespace preserves the local account key when the same trusted
// provider moves URL. Token verification still uses the configured HTTPS issuer.
func WithIdentityNamespace(store WebStore, verifiedIssuer, namespace string) WebStore {
	if namespace == "" || namespace == verifiedIssuer {
		return store
	}
	return identityNamespace{WebStore: store, verifiedIssuer: verifiedIssuer, namespace: namespace}
}

type identityNamespace struct {
	WebStore
	verifiedIssuer, namespace string
}

func (s identityNamespace) ResolveTenant(ctx context.Context, issuer, subject, name string) (string, error) {
	if issuer != s.verifiedIssuer {
		return "", errors.New("identity issuer mismatch")
	}
	return s.WebStore.ResolveTenant(ctx, s.namespace, subject, name)
}
