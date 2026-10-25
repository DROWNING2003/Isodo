package injection

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Proxy is one sandbox-scoped HTTP CONNECT proxy. Non-matching hosts are
// tunnelled untouched; matching hosts are terminated with a locally issued
// certificate so authentication headers can be added outside the guest.
type Proxy struct {
	sandboxID string
	token     string
	ca        *CA
	listener  net.Listener
	server    *http.Server
	rules     atomic.Pointer[[]Rule]
	transport *http.Transport
	logger    *slog.Logger

	closed       atomic.Bool
	statsMu      sync.Mutex
	injected     int64
	lastHost     string
	lastPath     string
	lastRule     string
	lastInjected time.Time
}

func NewProxy(sandboxID string, ca *CA, logger *slog.Logger) (*Proxy, error) {
	if ca == nil {
		return nil, errors.New("injection CA is required")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for injection proxy: %w", err)
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	proxy := &Proxy{
		sandboxID: sandboxID, token: token, ca: ca, listener: listener, transport: transport,
		logger: logger,
	}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:          nil,
	}
	proxy.SetRules(nil)
	go func() {
		if err := proxy.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("injection proxy stopped", "sandbox_id", sandboxID, "error", err)
		}
	}()
	return proxy, nil
}

func (p *Proxy) Port() int {
	if address, ok := p.listener.Addr().(*net.TCPAddr); ok {
		return address.Port
	}
	return 0
}

func (p *Proxy) SetRules(rules []Rule) {
	copied := make([]Rule, len(rules))
	copy(copied, rules)
	p.rules.Store(&copied)
}

func (p *Proxy) Rules() []Rule {
	value := p.rules.Load()
	if value == nil {
		return nil
	}
	copied := make([]Rule, len(*value))
	copy(copied, *value)
	return copied
}

func (p *Proxy) Close() error {
	p.closed.Store(true)
	if p.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = p.server.Shutdown(ctx)
	}
	return p.listener.Close()
}

// Stats returns a secret-free snapshot for the sandbox page.
type Stats struct {
	Injected     int64     `json:"injected"`
	LastHost     string    `json:"last_host,omitempty"`
	LastPath     string    `json:"last_path,omitempty"`
	LastRule     string    `json:"last_rule,omitempty"`
	LastInjected time.Time `json:"last_injected_at,omitempty"`
}

func (p *Proxy) Stats() Stats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	return Stats{
		Injected: p.injected, LastHost: p.lastHost, LastPath: p.lastPath,
		LastRule: p.lastRule, LastInjected: p.lastInjected,
	}
}

func (p *Proxy) record(host, path, rule string) {
	p.statsMu.Lock()
	p.injected++
	p.lastHost, p.lastPath, p.lastRule = host, path, rule
	p.lastInjected = time.Now()
	p.statsMu.Unlock()
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("isodo:"+p.token))
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="isodo-sandbox"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	r.Header.Del("Proxy-Authorization")
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handlePlain(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, "443"
	}
	if !p.hasHostRule(host) {
		p.tunnel(w, r, net.JoinHostPort(host, port))
		return
	}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "injection proxy cannot hijack connection", http.StatusInternalServerError)
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		return
	}
	certificate, err := p.ca.CertForHost(host)
	if err != nil {
		conn.Close()
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*certificate},
		NextProtos:   []string{"http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(r.Context()); err != nil {
		tlsConn.Close()
		return
	}
	defer tlsConn.Close()
	p.serveMITM(tlsConn, host, port)
}

func (p *Proxy) serveMITM(conn *tls.Conn, host, port string) {
	reader := bufio.NewReader(conn)
	for {
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		if request.URL == nil {
			request.URL = &url.URL{}
		}
		request.URL.Scheme = "https"
		request.URL.Host = net.JoinHostPort(host, port)
		request.RequestURI = ""
		request.Host = host
		if port != "443" {
			request.Host = net.JoinHostPort(host, port)
		}
		removeHopHeaders(request.Header)
		request.Header.Del("Proxy-Authorization")
		if strings.EqualFold(request.Header.Get("Expect"), "100-continue") {
			_, _ = io.WriteString(conn, "HTTP/1.1 100 Continue\r\n\r\n")
			request.Header.Del("Expect")
		}
		if rules := p.matchRequest(request, host); len(rules) > 0 {
			for _, rule := range rules {
				applyRule(request, rule)
			}
			p.record(host, request.URL.Path, rules[len(rules)-1].Name)
		}
		response, err := p.transport.RoundTrip(request)
		if err != nil {
			writeProxyError(conn, http.StatusBadGateway, "upstream request failed")
			return
		}
		if err := response.Write(conn); err != nil {
			response.Body.Close()
			return
		}
		response.Body.Close()
		if request.Close || response.Close {
			return
		}
	}
}

func (p *Proxy) handlePlain(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || !r.URL.IsAbs() {
		http.Error(w, "injection proxy expects an absolute request URI", http.StatusBadRequest)
		return
	}
	host := r.URL.Hostname()
	r.RequestURI = ""
	removeHopHeaders(r.Header)
	if rules := p.matchRequest(r, host); len(rules) > 0 {
		for _, rule := range rules {
			applyRule(r, rule)
		}
		p.record(host, r.URL.Path, rules[len(rules)-1].Name)
	}
	response, err := p.transport.RoundTrip(r)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, target string) {
	upstream, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		http.Error(w, "upstream connection failed", http.StatusBadGateway)
		return
	}
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		upstream.Close()
		return
	}
	if buffered != nil && buffered.Reader.Buffered() > 0 {
		if _, err := io.CopyN(upstream, buffered, int64(buffered.Reader.Buffered())); err != nil {
			conn.Close()
			upstream.Close()
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
	conn.Close()
	upstream.Close()
	<-done
}

func (p *Proxy) hasHostRule(host string) bool {
	for _, rule := range p.Rules() {
		for _, candidate := range ruleHosts(rule) {
			if strings.EqualFold(candidate, host) {
				return true
			}
		}
	}
	return false
}

func (p *Proxy) matchRequest(request *http.Request, host string) []Rule {
	var matched []Rule
	for _, rule := range p.Rules() {
		if matchRule(request, host, rule) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func matchRule(request *http.Request, host string, rule Rule) bool {
	if request.URL == nil || !hostMatches(rule, host) || !authorityMatches(request.URL, rule) {
		return false
	}
	if !pathMatches(request.URL.Path, rule.Spec.BaseURL) {
		return false
	}
	for name, want := range rule.Spec.IfHeaders {
		if request.Header.Get(name) != want {
			return false
		}
	}
	query := request.URL.Query()
	for name, want := range rule.Spec.IfQueries {
		if query.Get(name) != want {
			return false
		}
	}
	return true
}

func hostMatches(rule Rule, host string) bool {
	for _, candidate := range ruleHosts(rule) {
		if strings.EqualFold(candidate, host) {
			return true
		}
	}
	return false
}

func ruleHosts(rule Rule) []string {
	if rule.Spec.BaseURL == "" {
		switch rule.Spec.Type {
		case TypeOpenAI:
			return []string{"api.openai.com"}
		case TypeAnthropic:
			return []string{"api.anthropic.com"}
		case TypeGemini:
			return []string{"generativelanguage.googleapis.com"}
		case TypeQiniu:
			return []string{"api.qnaigc.com"}
		case TypeGitHub:
			return []string{"github.com", "api.github.com"}
		default:
			return nil
		}
	}
	parsed, err := url.Parse(rule.Spec.BaseURL)
	if err != nil {
		return nil
	}
	return []string{parsed.Hostname()}
}

// Match the entire origin, so an HTTPS credential cannot be injected into an
// HTTP request or another service listening on the same hostname.
func authorityMatches(target *url.URL, rule Rule) bool {
	scheme, port := "https", "443"
	if rule.Spec.BaseURL != "" {
		base, err := url.Parse(rule.Spec.BaseURL)
		if err != nil {
			return false
		}
		scheme, port = base.Scheme, base.Port()
		if port == "" {
			if scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
	}
	targetPort := target.Port()
	if targetPort == "" {
		if target.Scheme == "https" {
			targetPort = "443"
		} else {
			targetPort = "80"
		}
	}
	return target.Scheme == scheme && targetPort == port
}

func (p *Proxy) URL(host string) string {
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, fmt.Sprint(p.Port())), User: url.UserPassword("isodo", p.token)}).String()
}

func pathMatches(path, baseURL string) bool {
	if baseURL == "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	prefix := parsed.Path
	if prefix == "" || prefix == "/" {
		return true
	}
	if strings.HasSuffix(prefix, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(prefix, "*"))
	}
	return path == prefix
}

func applyRule(request *http.Request, rule Rule) {
	switch rule.Spec.Type {
	case TypeHTTP:
		for name, value := range rule.Secret.Headers {
			request.Header.Del(name)
			request.Header.Set(name, value)
		}
	case TypeOpenAI, TypeQiniu:
		request.Header.Del("Authorization")
		request.Header.Set("Authorization", "Bearer "+bearerValue(rule.Secret.APIKey))
	case TypeAnthropic:
		request.Header.Del("x-api-key")
		request.Header.Set("x-api-key", strings.TrimSpace(rule.Secret.APIKey))
		if request.Header.Get("anthropic-version") == "" {
			request.Header.Set("anthropic-version", "2023-06-01")
		}
	case TypeGemini:
		request.Header.Del("x-goog-api-key")
		request.Header.Set("x-goog-api-key", strings.TrimSpace(rule.Secret.APIKey))
	case TypeGitHub:
		request.Header.Del("Authorization")
		request.Header.Set("Authorization", "Bearer "+bearerValue(rule.Secret.Token))
	}
}

func bearerValue(value string) string {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"Bearer ", "bearer ", "token ", "Token "} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return value
}

func removeHopHeaders(header http.Header) {
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func writeProxyError(conn net.Conn, status int, message string) {
	response := &http.Response{
		StatusCode: status, ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader(message + "\n")),
	}
	response.ContentLength = int64(len(message) + 1)
	_ = response.Write(conn)
}

// SetUpstreamRootCAs is used by tests and by a future custom upstream. It
// replaces the roots used when the proxy connects to the real destination.
func (p *Proxy) SetUpstreamRootCAs(pool *x509.CertPool) {
	p.transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
