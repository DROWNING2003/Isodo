package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type memoryWebStore struct {
	states   map[string]OAuthState
	sessions map[string]Session
}

func newMemoryWebStore() *memoryWebStore {
	return &memoryWebStore{states: map[string]OAuthState{}, sessions: map[string]Session{}}
}
func (s *memoryWebStore) ResolveTenant(context.Context, string, string, string) (string, error) {
	return "tenant-a", nil
}
func (s *memoryWebStore) SaveOAuthState(_ context.Context, hash []byte, state OAuthState, _ time.Time) error {
	s.states[string(hash)] = state
	return nil
}
func (s *memoryWebStore) ConsumeOAuthState(_ context.Context, hash []byte) (OAuthState, error) {
	v, ok := s.states[string(hash)]
	delete(s.states, string(hash))
	if !ok {
		return v, ErrUnauthenticated
	}
	return v, nil
}
func (s *memoryWebStore) SaveSession(_ context.Context, hash []byte, session Session) error {
	s.sessions[string(hash)] = session
	return nil
}
func (s *memoryWebStore) GetSession(_ context.Context, hash []byte) (Session, error) {
	v, ok := s.sessions[string(hash)]
	if !ok {
		return v, ErrUnauthenticated
	}
	return v, nil
}
func (s *memoryWebStore) DeleteSession(_ context.Context, hash []byte) error {
	delete(s.sessions, string(hash))
	return nil
}

type testOIDC struct {
	url                        string
	key                        *rsa.PrivateKey
	nonce, challenge, audience string
	expiry                     time.Time
	exchanges                  int
}

func (p *testOIDC) sign(t *testing.T) string {
	t.Helper()
	encode := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	raw := encode(map[string]any{"alg": "RS256", "kid": "test"}) + "." + encode(map[string]any{"iss": p.url, "sub": "github-user", "aud": p.audience, "iat": time.Now().Unix(), "exp": p.expiry.Unix(), "nonce": p.nonce, "name": "User A"})
	digest := sha256.Sum256([]byte(raw))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return raw + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func newOIDCTest(t *testing.T) (*Web, *memoryWebStore, *testOIDC) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &testOIDC{key: key, audience: "isodo", expiry: time.Now().Add(time.Hour)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, 200, map[string]any{"issuer": p.url, "authorization_endpoint": p.url + "/authorize", "token_endpoint": p.url + "/token", "jwks_uri": p.url + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			writeJSON(w, 200, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
		case "/token":
			p.exchanges++
			_ = r.ParseForm()
			challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("client_id") != "isodo" || r.Form.Get("client_secret") != "private-secret" || base64.RawURLEncoding.EncodeToString(challenge[:]) != p.challenge || r.Form.Get("redirect_uri") != "http://127.0.0.1:5734/auth/callback" {
				t.Error("code exchange did not authenticate the client, callback and PKCE verifier")
				w.WriteHeader(400)
				return
			}
			writeJSON(w, 200, map[string]any{"access_token": "upstream-only", "token_type": "Bearer", "expires_in": 3600, "id_token": p.sign(t)})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	p.url = server.URL
	store := newMemoryWebStore()
	web, err := NewWeb(t.Context(), store, p.url, "isodo", "private-secret", "http://127.0.0.1:5734")
	if err != nil {
		t.Fatal(err)
	}
	return web, store, p
}
func startOAuth(t *testing.T, h *Web, p *testOIDC) (string, *http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	h.Login(w, httptest.NewRequest("GET", "http://127.0.0.1:5734/auth/login", nil))
	if w.Code != 302 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	p.nonce = u.Query().Get("nonce")
	p.challenge = u.Query().Get("code_challenge")
	if p.nonce == "" || p.challenge == "" || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("provider_hint") != "isodo-github" {
		t.Fatal("missing nonce/PKCE")
	}
	return u.Query().Get("state"), w.Result().Cookies()[0]
}
func TestWebOAuthSessionCSRFAndLogout(t *testing.T) {
	h, store, p := newOIDCTest(t)
	state, cookie := startOAuth(t, h, p)
	forged := httptest.NewRequest("GET", "http://127.0.0.1:5734/auth/callback?code=c&state="+state, nil)
	w := httptest.NewRecorder()
	h.Callback(w, forged)
	if len(store.sessions) != 0 || p.exchanges != 0 {
		t.Fatal("unbound callback was accepted")
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:5734/auth/callback?code=c&state="+state, nil)
	request.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.Callback(w, request)
	if w.Code != 303 || strings.Contains(w.Header().Get("Location"), "login_error") || len(store.sessions) != 1 {
		t.Fatalf("callback failed: %d %s", w.Code, w.Header().Get("Location"))
	}
	var sessionCookieValue *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			sessionCookieValue = c
		}
	}
	if sessionCookieValue == nil || !sessionCookieValue.HttpOnly || sessionCookieValue.SameSite != http.SameSiteLaxMode || sessionCookieValue.MaxAge <= 0 {
		t.Fatal("insecure session cookie")
	}
	if _, ok := store.sessions[sessionCookieValue.Value]; ok {
		t.Fatal("raw credential stored instead of hash")
	}
	replay := httptest.NewRecorder()
	h.Callback(replay, request)
	if !strings.Contains(replay.Header().Get("Location"), "login_error") || p.exchanges != 1 {
		t.Fatal("state replay accepted")
	}
	me := httptest.NewRequest("GET", "http://127.0.0.1:5734/auth/me", nil)
	me.AddCookie(sessionCookieValue)
	w = httptest.NewRecorder()
	h.GetSession(w, me)
	if w.Code != 200 || strings.Contains(w.Body.String(), "upstream-only") {
		t.Fatal("me response leaked upstream token or failed")
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	logout := httptest.NewRequest("POST", "http://127.0.0.1:5734/auth/logout", nil)
	logout.AddCookie(sessionCookieValue)
	w = httptest.NewRecorder()
	h.Logout(w, logout)
	if w.Code != 403 {
		t.Fatal("CSRF-less logout accepted")
	}
	logout.Header.Set("Origin", "https://attacker.example")
	logout.Header.Set("X-CSRF-Token", body.CSRF)
	w = httptest.NewRecorder()
	h.Logout(w, logout)
	if w.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	logout.Header.Set("Origin", "http://127.0.0.1:5734")
	w = httptest.NewRecorder()
	h.Logout(w, logout)
	if w.Code != 204 || len(store.sessions) != 0 {
		t.Fatal("logout did not revoke session")
	}
	w = httptest.NewRecorder()
	h.GetSession(w, me)
	if w.Code != 401 {
		t.Fatal("revoked session accepted")
	}
}
func TestWebRejectsInvalidIDTokens(t *testing.T) {
	h, store, p := newOIDCTest(t)
	for _, kind := range []string{"nonce", "audience", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			p.audience = "isodo"
			p.expiry = time.Now().Add(time.Hour)
			state, cookie := startOAuth(t, h, p)
			switch kind {
			case "nonce":
				p.nonce = "attacker"
			case "audience":
				p.audience = "other-app"
			case "expiry":
				p.expiry = time.Now().Add(-time.Hour)
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:5734/auth/callback?code=c&state="+state, nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.Callback(w, r)
			if !strings.Contains(w.Header().Get("Location"), "login_error") || len(store.sessions) != 0 {
				t.Fatal("invalid ID token created a session")
			}
		})
	}
}
func TestAPIKeyTokenParsing(t *testing.T) {
	id, secret, raw := newAPIKey()
	got, hash, ok := parseAPIKey(raw)
	if !ok || got != id || string(hash) != string(tokenHash(secret)) {
		t.Fatal("valid key rejected")
	}
	for _, input := range []string{raw + "x", strings.Replace(raw, "iso_", "other_", 1), "iso_abc_secret", "iso_" + id + "_" + strings.Repeat("!", 43)} {
		if _, _, ok := parseAPIKey(input); ok {
			t.Fatal("malformed key accepted")
		}
	}
	if validScopes([]string{"sandboxes:read", "sandboxes:read"}) || validScopes(nil) || validScopes([]string{"admin"}) {
		t.Fatal("invalid scopes accepted")
	}
}
