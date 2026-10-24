package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type identityStoreStub struct {
	issuer, subject, displayName string
	tenantID                     string
	err                          error
}

func (s *identityStoreStub) ResolveTenant(_ context.Context, issuer, subject, displayName string) (string, error) {
	s.issuer, s.subject, s.displayName = issuer, subject, displayName
	return s.tenantID, s.err
}

func TestCasdoorAuthenticatesUserInfoAndMapsStableSubject(t *testing.T) {
	store := &identityStoreStub{tenantID: "tenant-a"}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":            server.URL,
				"userinfo_endpoint": server.URL + "/userinfo",
			})
		case "/userinfo":
			if got := r.Header.Get("Authorization"); got != "Bearer signed-access-token" {
				t.Errorf("authorization = %q, want bearer token", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"iss": server.URL, "sub": "casdoor-subject-1", "aud": []string{"isodo-client"},
				"email": "not-used@example.test", "preferred_username": "alice",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	casdoor, err := NewCasdoor(context.Background(), server.URL, "isodo-client", store, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := casdoor.Authenticate(context.Background(), "Bearer signed-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if identity.TenantID != "tenant-a" || identity.Subject != "casdoor-subject-1" || identity.DisplayName != "alice" {
		t.Fatalf("identity = %#v", identity)
	}
	if store.issuer != server.URL || store.subject != "casdoor-subject-1" || store.displayName != "alice" {
		t.Fatalf("identity store received (%q, %q, %q)", store.issuer, store.subject, store.displayName)
	}
}

func TestCasdoorRejectsWrongAudienceAndReportsProviderOutage(t *testing.T) {
	t.Run("wrong audience", func(t *testing.T) {
		store := &identityStoreStub{tenantID: "tenant-a"}
		server := newCasdoorTestServer(t, http.StatusOK, `{"iss":"http://127.0.0.1","sub":"subject","aud":"some-other-client"}`)
		defer server.Close()
		casdoor := testCasdoor(t, server, store)
		if _, err := casdoor.Authenticate(context.Background(), "Bearer token"); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("Authenticate error = %v, want ErrUnauthenticated", err)
		}
	})

	t.Run("provider outage", func(t *testing.T) {
		store := &identityStoreStub{tenantID: "tenant-a"}
		server := newCasdoorTestServer(t, http.StatusServiceUnavailable, `{}`)
		defer server.Close()
		casdoor := testCasdoor(t, server, store)
		if _, err := casdoor.Authenticate(context.Background(), "Bearer token"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Authenticate error = %v, want ErrUnavailable", err)
		}
	})
}

func newCasdoorTestServer(t *testing.T, status int, userInfoBody string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": server.URL, "userinfo_endpoint": server.URL + "/userinfo"})
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.ReplaceAll(userInfoBody, "http://127.0.0.1", server.URL)))
	}))
	return server
}

func testCasdoor(t *testing.T, server *httptest.Server, store IdentityStore) *Casdoor {
	t.Helper()
	casdoor, err := NewCasdoor(context.Background(), server.URL, "isodo-client", store, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return casdoor
}
