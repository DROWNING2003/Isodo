package mcpmarket

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorResponseDeclaresUTF8(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, 400, "MCP_AUTHORIZATION_FAILED")
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(w.Body.String(), "MCP 操作失败") {
		t.Fatal("missing readable Chinese error")
	}
}
