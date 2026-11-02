package mcpmarket

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestCustomMCPEndpointBoundary(t *testing.T) {
	for _, value := range []string{"http://public.example/mcp", "https://user:password@public.example/mcp", "https://public.example/mcp?token=secret", "https://public.example:8443/mcp", "https://127.0.0.1/mcp", "https://[::1]/mcp", "https://[::ffff:127.0.0.1]/mcp", "https://169.254.169.254/mcp", "https://localhost/mcp", "https://10.210.10.32/mcp", "https://100.64.0.1/mcp", "https://198.18.0.1/mcp"} {
		if validCustomEndpoint(value) {
			t.Errorf("accepted unsafe endpoint %q", value)
		}
	}
	if !validCustomEndpoint("https://mcp.example.org/service") {
		t.Fatal("public HTTPS rejected")
	}
	for _, value := range []string{"10.0.0.1", "127.0.0.1", "169.254.169.254", "192.0.2.1", "100.64.0.1", "198.18.1.1", "::ffff:192.168.1.1", "2001:db8::1", "64:ff9b::a00:1"} {
		if publicMCPAddress(netip.MustParseAddr(value)) {
			t.Errorf("unsafe DNS result accepted: %s", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicMCPAddress(netip.MustParseAddr(value)) {
			t.Errorf("public address rejected: %s", value)
		}
	}
	client := newCustomHTTPClient("https://127.0.0.1/mcp")
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "POST", "https://127.0.0.1/mcp", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("private dial was allowed")
	}
}
func TestCustomMCPSessionOnly(t *testing.T) {
	h := &Handler{}
	request := httptest.NewRequest("POST", "/v1/mcp/custom", nil)
	out := httptest.NewRecorder()
	h.Account(out, request)
	if out.Code != 401 {
		t.Fatalf("anonymous custom creation: %d", out.Code)
	}
}
