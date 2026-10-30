package preview

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPublishAuthenticateAndProxy(t *testing.T) {
	var upstreamHost, upstreamCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		upstreamCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, "preview-ok")
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	publisher, err := New(Config{Secret: "test-secret", Domain: "localhost", Scheme: "http", APIPort: "43123", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	const sandboxID = "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	nodeURL := "http://8080-" + sandboxID + ".localhost:" + backendURL.Port() + "/pair#token=pair-token"
	base, pairing, err := publisher.Publish(sandboxID, 8080, nodeURL)
	if err != nil {
		t.Fatal(err)
	}
	if base != "http://8080-"+sandboxID+".localhost:43123/" {
		t.Fatalf("base = %q", base)
	}
	parsed, err := url.Parse(pairing)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(parsed.Path, authPrefix)
	if token == "" || parsed.Query().Get("next") != "/pair" || parsed.Fragment != "token=pair-token" {
		t.Fatalf("pairing = %q", pairing)
	}

	handler := publisher.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected fallthrough", http.StatusTeapot)
	}))
	host := "8080-" + sandboxID + ".localhost:43123"

	authRequest := httptest.NewRequest(http.MethodGet, "http://"+host+authPrefix+token+"?next=/pair", nil)
	authResponse := httptest.NewRecorder()
	handler.ServeHTTP(authResponse, authRequest)
	if authResponse.Code != http.StatusFound || authResponse.Header().Get("Location") != "/pair" {
		t.Fatalf("auth status=%d location=%q", authResponse.Code, authResponse.Header().Get("Location"))
	}
	cookies := authResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("cookies = %#v", cookies)
	}

	proxyRequest := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	proxyRequest.AddCookie(cookies[0])
	proxyResponse := httptest.NewRecorder()
	handler.ServeHTTP(proxyResponse, proxyRequest)
	if proxyResponse.Code != http.StatusOK || proxyResponse.Body.String() != "preview-ok" {
		t.Fatalf("proxy status=%d body=%q", proxyResponse.Code, proxyResponse.Body.String())
	}
	if upstreamHost != host {
		t.Fatalf("upstream Host = %q, want %q", upstreamHost, host)
	}
	if strings.Contains(upstreamCookie, cookieName) || strings.Contains(upstreamCookie, "isodo_session") {
		t.Fatalf("upstream received internal cookie: %q", upstreamCookie)
	}
}

func TestPreviewRejectsMissingOrForeignCredential(t *testing.T) {
	publisher, err := New(Config{Secret: "test-secret", Domain: "localhost", Scheme: "http", APIPort: "43123"})
	if err != nil {
		t.Fatal(err)
	}
	const sandboxID = "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	handler := publisher.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://8080-"+sandboxID+".localhost:43123/", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}

	otherToken := publisher.mint("f323e70d-cd11-4a02-a245-000000000000", 8080, time.Now().Add(time.Minute))
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://8080-"+sandboxID+".localhost:43123/", nil)
	request.AddCookie(&http.Cookie{Name: cookieName, Value: otherToken})
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("foreign token status = %d", response.Code)
	}
}

func TestPreviewPassesNonPreviewHosts(t *testing.T) {
	publisher, err := New(Config{Secret: "test-secret", Domain: "localhost", Scheme: "http", APIPort: "43123"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := publisher.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://api.localhost:43123/v1/sandboxes", nil))
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("fallthrough called=%v status=%d", called, response.Code)
	}
}

type fakeRelay struct {
	address string
}

func (f fakeRelay) RelayPort(ctx context.Context, _ string, _ uint64, _ uint16, _ string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", f.address)
}

func TestPreviewRelayDialsNodeWithoutLocalTarget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "relay-ok")
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := New(Config{Secret: "test-secret", Domain: "localhost", Scheme: "http", APIPort: "43123", Relay: fakeRelay{address: backendURL.Host}})
	if err != nil {
		t.Fatal(err)
	}
	const sandboxID = "7f0f1f64-9b3c-4c0f-8a9e-0d1e2f3a4b5c"
	_, pairing, err := publisher.Publish(sandboxID, 8080, "http://8080-"+sandboxID+".localhost:9999/pair#token=relay-token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(pairing)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(parsed.Path, authPrefix)
	host := "8080-" + sandboxID + ".localhost:43123"
	auth := httptest.NewRequest(http.MethodGet, "http://"+host+authPrefix+token+"?next=/pair", nil)
	authResponse := httptest.NewRecorder()
	publisher.Wrap(http.NotFoundHandler()).ServeHTTP(authResponse, auth)
	cookies := authResponse.Result().Cookies()
	if authResponse.Code != http.StatusFound || len(cookies) != 1 {
		t.Fatalf("auth status=%d cookies=%d", authResponse.Code, len(cookies))
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	publisher.Wrap(http.NotFoundHandler()).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "relay-ok" {
		t.Fatalf("relay proxy status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestPreviewRelaySurvivesPublisherRestartForExistingPairing(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "relay-after-restart")
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	config := Config{
		Secret:  "test-secret",
		Domain:  "localhost",
		Scheme:  "http",
		APIPort: "43123",
		Relay:   fakeRelay{address: backendURL.Host},
	}
	firstPublisher, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	const sandboxID = "4e59eb96-35e9-4f22-88d9-2a3f4097b62a"
	_, pairing, err := firstPublisher.Publish(sandboxID, 8080, "http://8080-"+sandboxID+".localhost:9999/pair#token=relay-token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(pairing)
	if err != nil {
		t.Fatal(err)
	}
	secondPublisher, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	handler := secondPublisher.Wrap(http.NotFoundHandler())
	host := "8080-" + sandboxID + ".localhost:43123"

	auth := httptest.NewRequest(http.MethodGet, "http://"+host+parsed.EscapedPath()+"?next=/pair", nil)
	authResponse := httptest.NewRecorder()
	handler.ServeHTTP(authResponse, auth)
	cookies := authResponse.Result().Cookies()
	if authResponse.Code != http.StatusFound || len(cookies) != 1 {
		t.Fatalf("auth status=%d cookies=%d", authResponse.Code, len(cookies))
	}

	request := httptest.NewRequest(http.MethodGet, "http://"+host+"/.well-known/t3/environment", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "relay-after-restart" {
		t.Fatalf("relay proxy after restart status=%d body=%q", response.Code, response.Body.String())
	}
}
