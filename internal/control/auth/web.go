package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	adminapi "github.com/DROWNING2003/Isodo/internal/api/admin"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const sessionCookie = "isodo_session"
const stateCookie = "isodo_oauth_state"

type OAuthState struct{ Nonce, Verifier string }
type Session struct {
	Identity  Identity
	CSRF      string
	ExpiresAt time.Time
}
type WebStore interface {
	IdentityStore
	SaveOAuthState(context.Context, []byte, OAuthState, time.Time) error
	ConsumeOAuthState(context.Context, []byte) (OAuthState, error)
	SaveSession(context.Context, []byte, Session) error
	GetSession(context.Context, []byte) (Session, error)
	DeleteSession(context.Context, []byte) error
}

type Web struct {
	store          WebStore
	oauth          oauth2.Config
	verifier       *oidc.IDTokenVerifier
	origin, issuer string
	secure         bool
	client         *http.Client
	limiterMu      sync.Mutex
	loginAttempts  map[string]loginWindow
}
type loginWindow struct {
	Start time.Time
	Count int
}

func NewWeb(ctx context.Context, store WebStore, issuer, clientID, secret, siteOrigin string) (*Web, error) {
	return NewWebWithPolicy(ctx, store, issuer, clientID, secret, siteOrigin, ProviderEndpointPolicy{})
}

// NewWebWithPolicy configures web login using the supplied endpoint policy.
func NewWebWithPolicy(ctx context.Context, store WebStore, issuer, clientID, secret, siteOrigin string, policy ProviderEndpointPolicy) (*Web, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	site, err := url.Parse(siteOrigin)
	if err != nil || site.Host == "" || site.User != nil || site.RawQuery != "" || site.Fragment != "" || (site.Path != "" && site.Path != "/") || (site.Scheme != "https" && !(site.Scheme == "http" && isLoopbackHost(site.Hostname()))) {
		return nil, errors.New("ISODO_WEB_ORIGIN must be HTTPS or loopback HTTP with no path")
	}
	if secret == "" {
		return nil, errors.New("ISODO_CASDOOR_CLIENT_SECRET is required for web login")
	}
	if !validProviderEndpoint(issuer, policy) {
		return nil, errors.New("invalid Casdoor issuer")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, errors.New("Casdoor OIDC discovery failed")
	}
	endpoint := provider.Endpoint()
	if !validProviderEndpoint(endpoint.AuthURL, policy) || !validProviderEndpoint(endpoint.TokenURL, policy) {
		return nil, errors.New("invalid OAuth endpoint")
	}
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	origin := strings.TrimRight(siteOrigin, "/")
	return &Web{store: store, oauth: oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: endpoint, RedirectURL: origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, verifier: provider.Verifier(&oidc.Config{ClientID: clientID}), origin: origin, issuer: issuer, secure: site.Scheme == "https", client: client, loginAttempts: make(map[string]loginWindow)}, nil
}

func (h *Web) setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func (h *Web) allowLogin(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	h.limiterMu.Lock()
	defer h.limiterMu.Unlock()
	now := time.Now()
	for k, v := range h.loginAttempts {
		if now.Sub(v.Start) > time.Minute {
			delete(h.loginAttempts, k)
		}
	}
	v := h.loginAttempts[host]
	if v.Start.IsZero() {
		v.Start = now
	}
	if v.Count >= 20 || len(h.loginAttempts) >= 4096 {
		return false
	}
	v.Count++
	h.loginAttempts[host] = v
	return true
}
func (h *Web) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.allowLogin(r) {
		writeAuthError(w, 429, "TOO_MANY_ATTEMPTS", "Please wait before signing in again.")
		return
	}
	state, nonce, verifier := randomToken(), randomToken(), oauth2.GenerateVerifier()
	if err := h.store.SaveOAuthState(r.Context(), tokenHash(state), OAuthState{Nonce: nonce, Verifier: verifier}, time.Now().Add(10*time.Minute)); err != nil {
		writeAuthenticationError(w, err)
		return
	}
	h.setCookie(w, stateCookie, state, "/auth/callback", 600)
	http.Redirect(w, r, h.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("provider_hint", "isodo-github")), http.StatusFound)
}
func (h *Web) Callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	fail := func() { http.Redirect(w, r, h.origin+"/sandboxes?login_error=oauth_failed", http.StatusSeeOther) }
	cookies := r.CookiesNamed(stateCookie)
	states, codes := r.URL.Query()["state"], r.URL.Query()["code"]
	if len(cookies) != 1 || len(states) != 1 || len(states[0]) != 43 || subtle.ConstantTimeCompare([]byte(cookies[0].Value), []byte(states[0])) != 1 {
		fail()
		return
	}
	h.setCookie(w, stateCookie, "", "/auth/callback", -1)
	state, err := h.store.ConsumeOAuthState(r.Context(), tokenHash(states[0]))
	if err != nil {
		fail()
		return
	}
	if len(codes) != 1 || codes[0] == "" || len(codes[0]) > 8192 || r.URL.Query().Get("error") != "" {
		fail()
		return
	}
	ctx := oidc.ClientContext(r.Context(), h.client)
	token, err := h.oauth.Exchange(ctx, codes[0], oauth2.VerifierOption(state.Verifier))
	if err != nil {
		fail()
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		fail()
		return
	}
	idToken, err := h.verifier.Verify(ctx, raw)
	if err != nil || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(state.Nonce)) != 1 || idToken.Subject == "" || len(idToken.Subject) > 1024 {
		fail()
		return
	}
	var profile struct {
		Name      string `json:"name"`
		Preferred string `json:"preferred_username"`
	}
	if idToken.Claims(&profile) != nil {
		fail()
		return
	}
	name := profile.Preferred
	if name == "" {
		name = profile.Name
	}
	if len(name) > 256 {
		name = name[:256]
	}
	tenant, err := h.store.ResolveTenant(ctx, h.issuer, idToken.Subject, name)
	if err != nil {
		fail()
		return
	}
	expires := time.Now().Add(12 * time.Hour)
	if idToken.Expiry.Before(expires) {
		expires = idToken.Expiry
	}
	session := Session{Identity: Identity{Issuer: h.issuer, Subject: idToken.Subject, DisplayName: name, TenantID: tenant, Credential: "session"}, CSRF: randomToken(), ExpiresAt: expires}
	rawSession := randomToken()
	if h.store.SaveSession(ctx, tokenHash(rawSession), session) != nil {
		fail()
		return
	}
	if old, err := r.Cookie(sessionCookie); err == nil {
		_ = h.store.DeleteSession(ctx, tokenHash(old.Value))
	}
	h.setCookie(w, sessionCookie, rawSession, "/", max(1, int(time.Until(expires).Seconds())))
	http.Redirect(w, r, h.origin+"/sandboxes", http.StatusSeeOther)
}

func (h *Web) session(r *http.Request) (Session, error) {
	cookies := r.CookiesNamed(sessionCookie)
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		return Session{}, ErrUnauthenticated
	}
	session, err := h.store.GetSession(r.Context(), tokenHash(cookies[0].Value))
	if err != nil {
		return Session{}, err
	}
	if !session.ExpiresAt.After(time.Now()) {
		return Session{}, ErrUnauthenticated
	}
	session.Identity.Credential = "session"
	return session, nil
}
func (h *Web) authenticate(r *http.Request) (Identity, error) {
	session, err := h.session(r)
	if err != nil {
		return Identity{}, err
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		csrf := r.Header.Values("X-CSRF-Token")
		if r.Header.Get("Origin") != h.origin || len(csrf) != 1 || subtle.ConstantTimeCompare([]byte(csrf[0]), []byte(session.CSRF)) != 1 {
			return Identity{}, ErrCSRF
		}
	}
	return session.Identity, nil
}
func (h *Web) GetSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.session(r)
	if err != nil {
		writeAuthenticationError(w, err)
		return
	}
	writeJSON(w, 200, adminapi.Session{User: adminapi.Account{Name: session.Identity.DisplayName, Provider: "GitHub"}, CsrfToken: session.CSRF, ExpiresAt: session.ExpiresAt})
}
func (h *Web) Logout(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authenticate(r); err != nil {
		writeAuthenticationError(w, err)
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	if err := h.store.DeleteSession(r.Context(), tokenHash(cookie.Value)); err != nil {
		writeAuthenticationError(w, err)
		return
	}
	h.setCookie(w, sessionCookie, "", "/", -1)
	w.WriteHeader(204)
}

type APIKeyHandler struct{ store KeyStore }

func NewAPIKeyHandler(store KeyStore) *APIKeyHandler { return &APIKeyHandler{store: store} }

func APIKeyManagementMiddleware(access *Access, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if len(r.Header.Values("X-API-Key")) > 0 {
			writeAuthenticationError(w, ErrUnauthenticated)
			return
		}
		Middleware(access, next).ServeHTTP(w, r)
	})
}

func (h *APIKeyHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	identity, _ := IdentityFromContext(r.Context())
	keys, err := h.store.ListAPIKeys(r.Context(), identity.TenantID)
	if err != nil {
		writeAuthenticationError(w, err)
		return
	}
	writeJSON(w, 200, adminapi.APIKeyList{Items: keys})
}

func (h *APIKeyHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var input adminapi.CreateAPIKeyJSONRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAuthError(w, 400, "INVALID_REQUEST", "Invalid key request.")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAuthError(w, 400, "INVALID_REQUEST", "Expected one JSON object.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) < 1 || len(input.Name) > 64 || !validScopes(input.Scopes) || (input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now())) {
		writeAuthError(w, 400, "INVALID_REQUEST", "Name, scopes or expiry is invalid.")
		return
	}
	id, secret, raw := newAPIKey()
	identity, _ := IdentityFromContext(r.Context())
	key, err := h.store.CreateAPIKey(r.Context(), identity.TenantID, APIKey{ID: id, Name: input.Name, Prefix: "iso_" + id + "_…", Scopes: input.Scopes, ExpiresAt: input.ExpiresAt}, tokenHash(secret))
	if errors.Is(err, ErrKeyLimit) {
		writeAuthError(w, 409, "API_KEY_LIMIT", "Revoke an existing key before creating more (limit 10).")
		return
	}
	if err != nil {
		writeAuthenticationError(w, err)
		return
	}
	writeJSON(w, 201, adminapi.CreatedAPIKey{
		ID: key.ID, Name: key.Name, Prefix: key.Prefix, Scopes: key.Scopes,
		CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt,
		RevokedAt: key.RevokedAt, Secret: raw,
	})
}

func (h *APIKeyHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request, keyID string) {
	identity, _ := IdentityFromContext(r.Context())
	err := h.store.RevokeAPIKey(r.Context(), identity.TenantID, keyID)
	if errors.Is(err, ErrNotFound) {
		writeAuthError(w, 404, "API_KEY_NOT_FOUND", "API key was not found.")
		return
	}
	if err != nil {
		writeAuthenticationError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Web) Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
