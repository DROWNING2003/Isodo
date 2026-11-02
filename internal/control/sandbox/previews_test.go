package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

type previewTestNode struct {
	testCommandRuntime
	boot string
}

func (n *previewTestNode) InspectSandbox(_ context.Context, r *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error) {
	return &nodev1.SandboxInspection{SandboxId: r.SandboxId, OwnershipGeneration: r.OwnershipGeneration, ObservedState: "running", BootId: n.boot, GuestEpoch: "epoch"}, nil
}
func (n *previewTestNode) RelayHTTPPort(context.Context, *nodev1.RelayOpen) (net.Conn, error) {
	return nil, errors.New("must not dial")
}

type previewTestAuth struct{}

func (previewTestAuth) Authenticate(ctx context.Context, s string) (auth.Identity, error) {
	if s == "Bearer preview-key" {
		return auth.Identity{TenantID: "tenant-a", Subject: "user-a", Credential: "api_key", Scopes: []string{"sandboxes:preview"}}, nil
	}
	i, err := commandAuth{}.Authenticate(ctx, s)
	if err == nil && i.Subject == "" {
		i.Subject = i.TenantID
	}
	return i, err
}
func TestCreateHTTPPreviewOwnershipAndLifecycle(t *testing.T) {
	store := newMemoryStore()
	_, _ = store.SetPortPreview(t.Context(), "tenant-a", otherSandboxID, 3000, "private")
	_, _ = store.SetPortPreview(t.Context(), "tenant-a", otherSandboxID, 5173, "private")
	item := store.items[otherSandboxID]
	item.ObservedState, item.DesiredState = "running", "running"
	item.NodeID, item.Revision, item.OwnershipGeneration = "node-a", "1", 7
	store.items[otherSandboxID] = item
	node := &previewTestNode{boot: "boot"}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(node)
	p, _ := preview.New(preview.Config{Secret: "test", Domain: "drowning.top", Scheme: "https", PublicPort: "18443"})
	handler.SetHTTPPreview(p, node)
	p.ConfigureHTTP(handler.AuthorizeHTTPPreview, node)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: previewTestAuth{}})
	var link preview.HTTPLink
	for _, tc := range []struct {
		name, token, body string
		want              int
	}{
		{"owner", "Bearer user-a", `{"port":3000}`, 201},
		{"explicit scope", "Bearer preview-key", `{"port":5173}`, 201},
		{"write key", "Bearer writer", `{"port":3000}`, 403},
		{"execute key", "Bearer executor", `{"port":3000}`, 403},
		{"cross tenant", "Bearer user-b", `{"port":3000}`, 404},
		{"anonymous", "", `{"port":3000}`, 401},
		{"management", "Bearer user-a", `{"port":31024}`, 400},
		{"workbench", "Bearer user-a", `{"port":38080}`, 400},
		{"overflow", "Bearer user-a", `{"port":65536}`, 400},
		{"unknown host", "Bearer user-a", `{"port":3000,"host":"127.0.0.1"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/sandboxes/"+otherSandboxID+"/previews", strings.NewReader(tc.body))
			r.Header.Set("Authorization", tc.token)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 201 {
				if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(link.URL, ":18443/") {
					t.Fatal("missing external port")
				}
			}
		})
	}
	gateway := p.Wrap(http.NotFoundHandler())
	u, _ := url.Parse(link.AuthorizationURL)
	for _, change := range []string{"stop requested", "start revision", "delete", "reassign", "Guest reboot"} {
		t.Run(change, func(t *testing.T) {
			changed := item
			switch change {
			case "stop requested":
				changed.DesiredState = "stopped"
			case "start revision":
				changed.Revision = "2"
			case "delete":
				changed.Deletion = &sandbox.Deletion{}
			case "reassign":
				changed.NodeID = "node-b"
			case "Guest reboot":
				node.boot = "new-boot"
			}
			store.items[otherSandboxID] = changed
			r := httptest.NewRequest("GET", link.URL, nil)
			r.Header.Set("X-Isodo-Preview-Token", u.Fragment)
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("stale grant status=%d", w.Code)
			}
			store.items[otherSandboxID] = item
			node.boot = "boot"
		})
	}
}
