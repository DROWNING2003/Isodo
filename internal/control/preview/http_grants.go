package preview

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
)

const httpCookieName = "isodo_http_preview"
const httpAuthPath = "/__isodo/port-auth"
const HTTPGrantTTL = 5 * time.Minute

// HTTPGrant is a bearer capability limited to one account, port and runtime.
// Validation always consults Control and Node; signature validity alone is insufficient.
type HTTPGrant struct {
	Purpose    string `json:"purpose"`
	TenantID   string `json:"tenant_id"`
	Subject    string `json:"subject"`
	SandboxID  string `json:"sandbox_id"`
	NodeID     string `json:"node_id"`
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
	BootID     string `json:"boot_id"`
	GuestEpoch string `json:"guest_epoch"`
	Port       uint16 `json:"port"`
	Expires    int64  `json:"exp"`
}

type HTTPRelay interface {
	RelayHTTPPort(context.Context, *nodev1.RelayOpen) (net.Conn, error)
}
type HTTPAuthorizer func(context.Context, HTTPGrant) error

type HTTPLink struct {
	URL              string    `json:"url"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// ConfigureHTTP enables generic ports independently of the legacy T3 publisher.
// Configure once, before serving requests.
func (p *Publisher) ConfigureHTTP(authorize HTTPAuthorizer, relay HTTPRelay) {
	if p != nil {
		p.httpAuthorize = authorize
		p.httpRelay = relay
	}
}

func (p *Publisher) IssueHTTP(ctx context.Context, grant HTTPGrant) (HTTPLink, error) {
	base, err := p.HTTPPortBaseURL(grant.SandboxID, grant.Port)
	if err != nil {
		return HTTPLink{}, err
	}
	if p.httpAuthorize == nil || p.httpRelay == nil {
		return HTTPLink{}, errors.New("HTTP preview is unavailable")
	}
	grant.Purpose = "http-port"
	expires := time.Now().Add(HTTPGrantTTL).Truncate(time.Second)
	grant.Expires = expires.Unix()
	if !validHTTPGrant(grant) {
		return HTTPLink{}, errors.New("invalid HTTP preview grant")
	}
	if err := p.httpAuthorize(ctx, grant); err != nil {
		return HTTPLink{}, err
	}
	payload, _ := json.Marshal(grant)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + "." + p.sign("http-port:"+encoded)
	// Fragments never reach HTTP access logs or Referer headers.
	return HTTPLink{URL: base, AuthorizationURL: base + strings.TrimPrefix(httpAuthPath, "/") + "#" + token, ExpiresAt: expires}, nil
}

func validHTTPGrant(g HTTPGrant) bool {
	return g.Purpose == "http-port" && g.TenantID != "" && g.Subject != "" && uuidPattern.MatchString(g.SandboxID) && g.NodeID != "" && g.Generation > 0 && g.Revision > 0 && g.BootID != "" && g.GuestEpoch != "" && g.Port != 0 && g.Port != 1024 && g.Port != 8080 && g.Expires > time.Now().Unix() && g.Expires <= time.Now().Add(HTTPGrantTTL).Unix()
}
func (p *Publisher) verifyHTTP(token string) (HTTPGrant, bool) {
	if len(token) > 8192 {
		return HTTPGrant{}, false
	}
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(p.sign("http-port:"+encoded))) {
		return HTTPGrant{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	var g HTTPGrant
	if err != nil || json.Unmarshal(payload, &g) != nil || !validHTTPGrant(g) {
		return HTTPGrant{}, false
	}
	return g, true
}

// The token is removed from the fragment before exchange and never sent upstream.
const httpAuthPage = `<!doctype html><meta charset="utf-8"><title>Isodo preview</title><p id="status">正在授权预览…</p><script src="/__isodo/port-auth.js"></script>`
const httpAuthScript = `const token=location.hash.slice(1);history.replaceState(null,"","/__isodo/port-auth");fetch("/__isodo/port-auth",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token}),credentials:"same-origin"}).then(r=>{if(!r.ok)throw Error("预览授权无效或已过期");location.replace("/")}).catch(e=>document.getElementById("status").textContent=e.message);`

func (p *Publisher) serveHTTPPort(w http.ResponseWriter, r *http.Request, id string, port uint16) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.URL.Path == httpAuthPath && r.Method == http.MethodGet {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, httpAuthPage)
		return
	}
	if r.URL.Path == httpAuthPath+".js" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		io.WriteString(w, httpAuthScript)
		return
	}
	token := ""
	exchange := r.URL.Path == httpAuthPath && r.Method == http.MethodPost
	if exchange {
		// Browser exchange is same-origin only, including when T3 allows other app origins.
		if r.Header.Get("Origin") != p.scheme+"://"+r.Host {
			http.Error(w, "preview origin is required", 403)
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<10)).Decode(&input) != nil {
			http.Error(w, "invalid preview credential", 400)
			return
		}
		token = input.Token
	} else {
		token = r.Header.Get("X-Isodo-Preview-Token")
		if token == "" {
			if cookie, err := r.Cookie(httpCookieName); err == nil {
				token = cookie.Value
			}
		}
	}
	grant, ok := p.verifyHTTP(token)
	if !ok || grant.SandboxID != id || grant.Port != port {
		http.Error(w, "preview authorization required", 401)
		return
	}
	if p.httpAuthorize == nil || p.httpRelay == nil {
		http.Error(w, "HTTP preview unavailable", 503)
		return
	}
	if err := p.httpAuthorize(r.Context(), grant); err != nil {
		http.Error(w, "preview runtime is no longer authorized", 403)
		return
	}
	if exchange {
		http.SetCookie(w, &http.Cookie{Name: httpCookieName, Value: token, Path: "/", HttpOnly: true, Secure: p.scheme == "https", SameSite: http.SameSiteStrictMode, Expires: time.Unix(grant.Expires, 0)})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Same-site sibling previews are untrusted. Require the exact Origin on writes
	// and browser WebSocket handshakes, even if a T3 app origin is allowed elsewhere.
	origin := r.Header.Get("Origin")
	unsafe := r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS"
	if (origin != "" && origin != p.scheme+"://"+r.Host) || ((unsafe || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")) && origin == "" && r.Header.Get("X-Isodo-Preview-Token") == "") {
		http.Error(w, "preview origin is not allowed", 403)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(grant.Expires, 0))
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := p.httpAuthorize(ctx, grant); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
		return p.httpRelay.RelayHTTPPort(ctx, &nodev1.RelayOpen{SandboxId: id, OwnershipGeneration: grant.Generation, Revision: grant.Revision, BootId: grant.BootID, GuestEpoch: grant.GuestEpoch, Port: uint32(port), Service: "http-port"})
	}}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ModifyResponse: stripHTTPPreviewResponse,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "sandbox-relay"
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", p.scheme)
			pr.Out.Header.Del("X-Isodo-Preview-Token")
			stripInternalCookies(pr.Out)
		}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "sandbox preview target unreachable", 502)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

// Upstream HTTP responses cannot set parent-domain cookies or Isodo credentials.
func stripHTTPPreviewResponse(response *http.Response) error {
	stripUpstreamCORS(response)
	cookies := response.Cookies()
	response.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if strings.HasPrefix(cookie.Name, "isodo_") {
			continue
		}
		cookie.Domain = ""
		response.Header.Add("Set-Cookie", cookie.String())
	}
	return nil
}
