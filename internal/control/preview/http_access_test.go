package preview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicPrivateSwitchAndActiveStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: ready\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write([]byte(r.Method))
	}))
	defer upstream.Close()
	p, _ := New(Config{Secret: "test", Domain: "drowning.top", Scheme: "http"})
	var prepares atomic.Int32
	var version atomic.Uint64
	var private atomic.Bool
	p.ConfigureHTTP(func(_ context.Context, g HTTPGrant) error {
		if g.PolicyRevision != version.Load() || g.Public == private.Load() {
			return errors.New("policy changed")
		}
		return nil
	}, &httpTestRelay{address: strings.TrimPrefix(upstream.URL, "http://")})
	p.ConfigureHTTPAccess(func(context.Context, string, uint16) (HTTPAccess, error) {
		visibility := "public"
		if private.Load() {
			visibility = "private"
		}
		g := httpTestGrant()
		g.Public = !private.Load()
		g.PolicyRevision = version.Load()
		return HTTPAccess{PortPolicy: PortPolicy{Visibility: visibility, Revision: version.Load()}, Grant: g, Prepare: func(context.Context) (HTTPGrant, error) { prepares.Add(1); return g, nil }}, nil
	})
	gateway := httptest.NewServer(p.Wrap(http.NotFoundHandler()))
	defer gateway.Close()
	client := gateway.Client()
	client.Timeout = 2 * time.Second
	request := func(method, path, token string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, gateway.URL+path, nil)
		r.Host = "3000-" + portPreviewSandboxID + ".drowning.top"
		r.Header.Set("X-Isodo-Preview-Token", token)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	check := func(want int, token string) {
		t.Helper()
		r := request("POST", "/", token)
		r.Body.Close()
		if want == 429 && r.Header.Get("Retry-After") != "1" {
			t.Fatal("missing retry hint")
		}
		if r.StatusCode != want {
			t.Fatalf("HTTP %d want %d", r.StatusCode, want)
		}
	}
	check(200, "") // No issuance call is needed to activate an unconfigured public port.
	g := httpTestGrant()
	g.Public = true
	link, err := p.IssueHTTP(t.Context(), g)
	if err != nil || link.Visibility != "public" || link.AuthorizationURL != "" || !link.ExpiresAt.IsZero() {
		t.Fatalf("public link=%+v err=%v", link, err)
	}
	check(200, "")
	stream := request("GET", "/events", "")
	defer stream.Body.Close()
	data := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(stream.Body, data); err != nil {
		t.Fatal(err)
	}
	private.Store(true)
	version.Add(1)
	p.CancelHTTPPort(portPreviewSandboxID, 3000)
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(stream.Body); done <- err }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("public stream survived private switch")
	}
	check(401, "")
	g = httpTestGrant()
	g.PolicyRevision = version.Load()
	link, err = p.IssueHTTP(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.AuthorizationURL)
	check(200, u.Fragment)
	private.Store(false)
	version.Add(1)
	p.CancelHTTPPort(portPreviewSandboxID, 3000)
	check(200, "")
	private.Store(true)
	version.Add(1)
	p.CancelHTTPPort(portPreviewSandboxID, 3000)
	check(401, u.Fragment)
	private.Store(false)
	version.Add(1)
	var streams []*http.Response
	for range 8 {
		stream := request("GET", "/events", "")
		if stream.StatusCode != 200 {
			t.Fatalf("public stream HTTP %d", stream.StatusCode)
		}
		if _, err := io.ReadFull(stream.Body, data); err != nil {
			t.Fatal(err)
		}
		streams = append(streams, stream)
		defer stream.Body.Close()
	}
	before := prepares.Load()
	check(429, "")
	if prepares.Load() != before {
		t.Fatal("rejected request inspected Node")
	}
	for _, stream := range streams {
		stream.Body.Close()
	}
}

func TestPublicAdmissionLimitsAndBoundedKeys(t *testing.T) {
	var a publicAdmission
	key := portKey{"sandbox", 3000}
	var held []func()
	for range 8 {
		release, ok := a.acquire(key)
		if !ok {
			t.Fatal("early connection rejection")
		}
		held = append(held, release)
	}
	if _, ok := a.acquire(key); ok {
		t.Fatal("ninth concurrent connection admitted")
	}
	for _, release := range held {
		release()
		release()
	}
	for range 12 {
		release, ok := a.acquire(key)
		if !ok {
			t.Fatal("early rate rejection")
		}
		release()
	}
	if _, ok := a.acquire(key); ok {
		t.Fatal("burst limit exceeded")
	}
	a.mu.Lock()
	a.buckets[key].updated = time.Now().Add(-time.Second)
	a.mu.Unlock()
	release, ok := a.acquire(key)
	if !ok {
		t.Fatal("rate did not refill")
	}
	release()
}

func TestReservedPortsCannotUsePublicResolver(t *testing.T) {
	p, _ := New(Config{Secret: "test", Domain: "drowning.top", Scheme: "https"})
	p.ConfigureHTTPAccess(func(context.Context, string, uint16) (HTTPAccess, error) {
		t.Error("reserved port reached public policy")
		return HTTPAccess{}, nil
	})
	for _, port := range []string{"31024", "38080", "31080", "1024", "1080", "8080"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://"+port+"-"+portPreviewSandboxID+".drowning.top/", nil)
		p.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("reserved %s HTTP %d", port, w.Code)
		}
	}
}
