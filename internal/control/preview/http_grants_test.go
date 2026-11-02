package preview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
)

type httpTestRelay struct {
	address string
	calls   atomic.Int32
}

func (r *httpTestRelay) RelayHTTPPort(ctx context.Context, open *nodev1.RelayOpen) (net.Conn, error) {
	if open.Service != "http-port" || open.Revision != 11 || open.OwnershipGeneration != 7 || open.BootId != "boot" || open.GuestEpoch != "epoch" {
		return nil, errors.New("unfenced relay")
	}
	r.calls.Add(1)
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.address)
	if err == nil {
		go func() { <-ctx.Done(); conn.Close() }()
	}
	return conn, err
}
func httpTestGrant() HTTPGrant {
	return HTTPGrant{TenantID: "tenant", Subject: "owner", SandboxID: portPreviewSandboxID, NodeID: "node", Generation: 7, Revision: 11, BootID: "boot", GuestEpoch: "epoch", Port: 3000}
}
func TestHTTPPreviewGrantIsolation(t *testing.T) {
	p, _ := New(Config{Secret: "test", Domain: "drowning.top", Scheme: "https"})
	var revoked atomic.Bool
	relay := &httpTestRelay{}
	p.ConfigureHTTP(func(context.Context, HTTPGrant) error {
		if revoked.Load() {
			return errors.New("runtime changed")
		}
		return nil
	}, relay)
	link, err := p.IssueHTTP(t.Context(), httpTestGrant())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.AuthorizationURL)
	token := u.Fragment
	if strings.Contains(u.Path, token) || link.ExpiresAt.Sub(time.Now()) > HTTPGrantTTL {
		t.Fatal("grant leaked into path or excessive TTL")
	}
	handler := p.Wrap(http.NotFoundHandler())
	for _, tc := range []struct {
		name, token, url string
		want             int
	}{
		{"URL is not authorization", "", link.URL, 401},
		{"tampered", token + "x", link.URL, 401},
		{"legacy T3 token", p.mint(portPreviewSandboxID, 3000, time.Now().Add(time.Hour)), link.URL, 401},
		{"wrong port", token, strings.Replace(link.URL, "3000-", "5173-", 1), 401},
		{"wrong sandbox", token, strings.Replace(link.URL, portPreviewSandboxID, "a323e70d-cd11-4a02-a245-6b4ed2ce1e31", 1), 401},
		{"reserved management", token, strings.Replace(link.URL, "3000-", "1024-", 1), 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.url, nil)
			r.Header.Set("X-Isodo-Preview-Token", tc.token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	if _, ok := p.verify(token); ok {
		t.Fatal("generic token accepted by legacy T3 verifier")
	}
	expired := httpTestGrant()
	expired.Purpose = "http-port"
	expired.Expires = time.Now().Unix() - 1
	payload, _ := json.Marshal(expired)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	if _, ok := p.verifyHTTP(encoded + "." + p.sign("http-port:"+encoded)); ok {
		t.Fatal("expired grant accepted")
	}
	for _, origin := range []string{"", "https://evil.drowning.top", "https://web.drowning.top"} {
		r := httptest.NewRequest("POST", link.URL+strings.TrimPrefix(httpAuthPath, "/"), strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-origin exchange status=%d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", link.URL+strings.TrimPrefix(httpAuthPath, "/"), strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://"+u.Host)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("exchange=%d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != httpCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure cookie: %s", cookie)
	}
	revoked.Store(true)
	r = httptest.NewRequest("GET", link.URL, nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 || relay.calls.Load() != 0 {
		t.Fatal("revoked runtime reached relay")
	}
}

func TestHTTPPreviewProxyHTTPAndSSE(t *testing.T) {
	requests := make(chan *http.Request, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(context.Background())
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Add("Set-Cookie", "app=ok; Domain=localhost; Path=/")
		w.Header().Add("Set-Cookie", "isodo_session=evil; Domain=localhost; Path=/")
		body, _ := io.ReadAll(r.Body)
		w.Write(append([]byte(r.URL.RequestURI()+":"), body...))
	}))
	defer upstream.Close()
	address := strings.TrimPrefix(upstream.URL, "http://")
	p, _ := New(Config{Secret: "test", Domain: "localhost", Scheme: "http"})
	var valid atomic.Bool
	valid.Store(true)
	relay := &httpTestRelay{address: address}
	p.ConfigureHTTP(func(context.Context, HTTPGrant) error {
		if !valid.Load() {
			return errors.New("stopped")
		}
		return nil
	}, relay)
	link, err := p.IssueHTTP(t.Context(), httpTestGrant())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.AuthorizationURL)
	gateway := httptest.NewServer(p.Wrap(http.NotFoundHandler()))
	defer gateway.Close()
	client := gateway.Client()
	client.Timeout = 3 * time.Second
	for _, method := range []string{"GET", "POST"} {
		r, _ := http.NewRequest(method, gateway.URL+"/hello?q=1", strings.NewReader("body"))
		r.Host = u.Host
		r.Header.Set("X-Isodo-Preview-Token", u.Fragment)
		r.Header.Set("Cookie", httpCookieName+"="+u.Fragment+"; isodo_session=secret; isodo_preview=secret; app=value")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || string(body) != "/hello?q=1:body" || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("proxy=%d %s", response.StatusCode, body)
		}
		cookies := response.Cookies()
		if len(cookies) != 1 || cookies[0].Name != "app" || cookies[0].Domain != "" {
			t.Fatalf("unsafe upstream cookies: %v", cookies)
		}
		received := <-requests
		if received.Header.Get("Cookie") != "app=value" || received.Header.Get("X-Isodo-Preview-Token") != "" || received.Host != u.Host {
			t.Fatal("credentials leaked or Host changed")
		}
	}
	r, _ := http.NewRequest("GET", gateway.URL+"/events", nil)
	r.Host = u.Host
	r.Header.Set("X-Isodo-Preview-Token", u.Fragment)
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-requests
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "data: first\n\n" {
		t.Fatal("SSE was buffered")
	}
	valid.Store(false)
	start := time.Now()
	_, err = io.ReadAll(response.Body)
	if time.Since(start) > time.Second {
		t.Fatalf("stream did not close on lifecycle change: %v", err)
	}
}
