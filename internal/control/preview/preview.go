// Package preview publishes a Sandbox Guest HTTP service through one API host.
// The public URL keeps the <port>-<sandboxID> host shape; the gateway verifies a
// short-lived signed cookie before proxying to the Node-owned target.
package preview

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName = "isodo_preview"
	authPrefix = "/__isodo/auth/"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Config struct {
	Secret    string
	Domain    string
	Scheme    string
	APIPort   string
	AppOrigin string
	TTL       time.Duration
	// Relay, when set, dials the Guest port through the Node gRPC stream
	// instead of assuming the API and Node share a loopback address.
	Relay RelayDialer
}

// RelayDialer is implemented by the Control Node client. The generation may
// be zero when the caller has no lifecycle generation available.
type RelayDialer interface {
	RelayPort(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error)
}

type Publisher struct {
	secret    []byte
	domain    string
	scheme    string
	apiPort   string
	appOrigin string
	ttl       time.Duration

	relay   RelayDialer
	mu      sync.RWMutex
	targets map[string]*url.URL
}

type claims struct {
	SandboxID string `json:"sandbox_id"`
	Port      uint16 `json:"port"`
	Expires   int64  `json:"exp"`
}

func New(config Config) (*Publisher, error) {
	if strings.TrimSpace(config.Secret) == "" || strings.TrimSpace(config.Domain) == "" {
		return nil, nil
	}
	scheme := strings.ToLower(strings.TrimSpace(config.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("preview scheme must be http or https")
	}
	if config.AppOrigin != "" {
		origin, err := url.Parse(config.AppOrigin)
		if err != nil || origin.Host == "" || origin.User != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return nil, errors.New("preview AppOrigin must be an HTTP origin")
		}
	}
	ttl := config.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Publisher{
		secret:    []byte(config.Secret),
		domain:    strings.Trim(strings.ToLower(config.Domain), "."),
		scheme:    scheme,
		apiPort:   strings.TrimSpace(config.APIPort),
		appOrigin: config.AppOrigin,
		ttl:       ttl,
		relay:     config.Relay,
		targets:   make(map[string]*url.URL),
	}, nil
}

// Publish records the Node target and returns the public base URL and the
// one-hop authenticated pairing URL. The pairing credential stays in the
// fragment and never reaches the API.
func (p *Publisher) Publish(sandboxID string, port uint16, nodeURL string) (string, string, error) {
	if p == nil {
		return "", "", errors.New("preview is not configured")
	}
	if !uuidPattern.MatchString(sandboxID) || port == 0 {
		return "", "", errors.New("invalid Sandbox preview target")
	}
	parsed, err := url.Parse(nodeURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Fragment == "" {
		return "", "", errors.New("invalid Node application link")
	}
	target := *parsed
	target.Path = ""
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	if strings.HasSuffix(parsed.Hostname(), ".localhost") || parsed.Hostname() == "127.0.0.1" {
		if parsed.Port() == "" {
			return "", "", errors.New("Node application link has no port")
		}
		target.Host = net.JoinHostPort("127.0.0.1", parsed.Port())
	}
	target.Scheme = "http"

	publicHost := fmt.Sprintf("%d-%s.%s", port, sandboxID, p.domain)
	if p.domain == "localhost" && p.apiPort != "" {
		publicHost = net.JoinHostPort(publicHost, p.apiPort)
	} else if p.scheme == "http" && p.apiPort != "" {
		publicHost = net.JoinHostPort(publicHost, p.apiPort)
	}
	token := p.mint(sandboxID, port, time.Now().Add(p.ttl))

	p.mu.Lock()
	if p.relay != nil {
		delete(p.targets, sandboxID)
	} else {
		p.targets[sandboxID] = &target
	}
	p.mu.Unlock()

	base := p.scheme + "://" + publicHost + "/"
	pairing := p.scheme + "://" + publicHost + authPrefix + token + "?next=/pair#" + parsed.Fragment
	return base, pairing, nil
}

func (p *Publisher) proxyRelay(w http.ResponseWriter, r *http.Request, sandboxID string, port uint16) {
	proxy := &httputil.ReverseProxy{
		ModifyResponse: stripUpstreamCORS,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "sandbox-relay"
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			stripInternalCookies(pr.Out)
		},
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return p.relay.RelayPort(ctx, sandboxID, 0, port, "t3")
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "sandbox preview target unreachable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func (p *Publisher) target(sandboxID string) (*url.URL, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	target, ok := p.targets[sandboxID]
	return target, ok
}

// Wrap serves authenticated preview hosts and passes every other request to next.
func (p *Publisher) Wrap(next http.Handler) http.Handler {
	if p == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sandboxID, port, ok := p.parseHost(r.Host)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Add("Vary", "Origin")
			if origin != p.appOrigin && origin != p.scheme+"://"+r.Host {
				http.Error(w, "preview origin is not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, authPrefix) {
			p.authenticate(w, r, sandboxID, port)
			return
		}
		current, ok := p.readCookie(r)
		if !ok || current.SandboxID != sandboxID || current.Port != port {
			http.Error(w, "preview authorization required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/__isodo/ready" {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if p.relay != nil {
			// The signed preview host already binds this request to the sandbox
			// and service port. Derive the relay target from it so existing
			// preview cookies continue working after an API process restart.
			p.proxyRelay(w, r, sandboxID, port)
			return
		}
		target, ok := p.target(sandboxID)
		if !ok {
			http.Error(w, "sandbox preview is not ready", http.StatusBadGateway)
			return
		}
		p.proxy(w, r, target)
	})
}

func (p *Publisher) authenticate(w http.ResponseWriter, r *http.Request, sandboxID string, port uint16) {
	token := strings.TrimPrefix(r.URL.Path, authPrefix)
	current, ok := p.verify(token)
	if !ok || current.SandboxID != sandboxID || current.Port != port {
		http.Error(w, "invalid or expired preview credential", http.StatusForbidden)
		return
	}
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   p.scheme == "https",
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(current.Expires, 0),
	})
	http.Redirect(w, r, next, http.StatusFound)
}

func (p *Publisher) readCookie(r *http.Request) (claims, bool) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return claims{}, false
	}
	return p.verify(cookie.Value)
}

func (p *Publisher) proxy(w http.ResponseWriter, r *http.Request, target *url.URL) {
	proxy := &httputil.ReverseProxy{
		ModifyResponse: stripUpstreamCORS,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			stripInternalCookies(pr.Out)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "sandbox preview target unreachable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// The gateway owns the browser origin policy; upstream wildcard CORS must not
// produce duplicate headers or broaden access to a credentialed preview.
func stripUpstreamCORS(response *http.Response) error {
	response.Header.Del("Access-Control-Allow-Origin")
	response.Header.Del("Access-Control-Allow-Credentials")
	return nil
}

func stripInternalCookies(request *http.Request) {
	headers := request.Header.Values("Cookie")
	if len(headers) == 0 {
		return
	}
	kept := make([]string, 0)
	for _, header := range headers {
		for _, item := range strings.Split(header, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(item), "=")
			if name == cookieName || name == "isodo_session" {
				continue
			}
			if strings.TrimSpace(item) != "" {
				kept = append(kept, strings.TrimSpace(item))
			}
		}
	}
	if len(kept) == 0 {
		request.Header.Del("Cookie")
		return
	}
	request.Header.Set("Cookie", strings.Join(kept, "; "))
}

func (p *Publisher) parseHost(hostport string) (string, uint16, bool) {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	suffix := "." + p.domain
	if !strings.HasSuffix(host, suffix) {
		return "", 0, false
	}
	label := strings.TrimSuffix(host, suffix)
	index := strings.IndexByte(label, '-')
	if index <= 0 || index == len(label)-1 {
		return "", 0, false
	}
	port, err := strconv.ParseUint(label[:index], 10, 16)
	if err != nil || port == 0 {
		return "", 0, false
	}
	sandboxID := label[index+1:]
	if !uuidPattern.MatchString(sandboxID) {
		return "", 0, false
	}
	return sandboxID, uint16(port), true
}

func (p *Publisher) mint(sandboxID string, port uint16, expires time.Time) string {
	payload, _ := json.Marshal(claims{SandboxID: sandboxID, Port: port, Expires: expires.Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + p.sign(encoded)
}

func (p *Publisher) verify(token string) (claims, bool) {
	encoded, signature, found := strings.Cut(token, ".")
	if !found || !hmac.Equal([]byte(signature), []byte(p.sign(encoded))) {
		return claims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return claims{}, false
	}
	var current claims
	if err := json.Unmarshal(payload, &current); err != nil || current.SandboxID == "" || current.Port == 0 || time.Now().Unix() > current.Expires {
		return claims{}, false
	}
	return current, true
}

func (p *Publisher) sign(encoded string) string {
	mac := hmac.New(sha256.New, p.secret)
	_, _ = mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
