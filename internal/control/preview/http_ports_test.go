package preview

import (
	"strings"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

const portPreviewSandboxID = "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"

func TestHTTPPortBaseURLMatchesExistingPreviewHostShape(t *testing.T) {
	p, err := New(Config{
		Secret: "test-secret",
		Domain: "drowning.top",
		Scheme: "https",
		PublicPort: "18443",
		APIPort: "5004",
	})
	if err != nil || p == nil {
		t.Fatalf("publisher = %v, err = %v", p, err)
	}
	for _, port := range []uint16{3000, 5173, 8000} {
		got, err := p.HTTPPortBaseURL(portPreviewSandboxID, port)
		if err != nil {
			t.Fatalf("port %d: %v", port, err)
		}
		want := "https://" + stringPort(port) + "-" + portPreviewSandboxID + ".drowning.top:18443/"
		if got != want {
			t.Fatalf("port %d: URL = %q, want %q", port, got, want)
		}
		id, parsedPort, ok := p.parseHost(strings.TrimPrefix(strings.TrimSuffix(got, "/"), "https://"))
		if !ok || id != portPreviewSandboxID || parsedPort != port {
			t.Fatalf("port %d: generated URL cannot be parsed by existing gateway", port)
		}
	}
}

func TestHTTPPortBaseURLUsesStandardHTTPSWhenPublicPortAbsent(t *testing.T) {
	p, err := New(Config{Secret: "test-secret", Domain: "drowning.top", Scheme: "https"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.HTTPPortBaseURL(portPreviewSandboxID, 3000)
	if err != nil || got != "https://3000-"+portPreviewSandboxID+".drowning.top/" {
		t.Fatalf("URL = %q, err = %v", got, err)
	}
}

func TestHTTPPortBaseURLRejectsManagementAndInvalidTargets(t *testing.T) {
	p, err := New(Config{Secret: "test-secret", Domain: "drowning.top", Scheme: "https"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		id string
		port uint16
	}{
		{"zero", portPreviewSandboxID, 0},
		{"Guest management", portPreviewSandboxID, 1024},
		{"T3 reserved", portPreviewSandboxID, 8080},
		{"invalid Sandbox ID", "not-a-sandbox", 3000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if url, err := p.HTTPPortBaseURL(tc.id, tc.port); err == nil || url != "" {
				t.Fatalf("url=%q, err=%v: should reject", url, err)
			}
		})
	}
	var unconfigured *Publisher
	if url, err := unconfigured.HTTPPortBaseURL(portPreviewSandboxID, 3000); err == nil || url != "" {
		t.Fatalf("unconfigured publisher returned %q, %v", url, err)
	}
}

func stringPort(port uint16) string {
	return strconv.FormatUint(uint64(port), 10)
}

func TestDeterministicPortURLNeverBypassesPreviewAuthorization(t *testing.T) {
	p, err := New(Config{Secret: "test-secret", Domain: "drowning.top", Scheme: "https"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := p.HTTPPortBaseURL(portPreviewSandboxID, 3000)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	gateway := p.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	result := httptest.NewRecorder()
	gateway.ServeHTTP(result, httptest.NewRequest(http.MethodGet, base, nil))
	if called || result.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned stable URL was allowed: status=%d fallthrough=%v", result.Code, called)
	}
}
