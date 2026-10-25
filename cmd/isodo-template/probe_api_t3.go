package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

type apiT3ProbeResult struct {
	SandboxID          string   `json:"sandbox_id"`
	CreateToReadyMS    int64    `json:"create_to_ready_ms"`
	AppLinkStatus      int      `json:"app_link_status"`
	AppLinkMS          int64    `json:"app_link_ms"`
	PublicURL          string   `json:"public_url"`
	AppHTTPStatus      int      `json:"app_http_status"`
	AppHTMLOK          bool     `json:"app_html_ok"`
	Capabilities       []string `json:"capabilities"`
	TerminalAdvertised bool     `json:"terminal_advertised"`
	RelayUsed          bool     `json:"relay_used"`
	Stopped            bool     `json:"stopped"`
	Deleted            bool     `json:"deleted"`
}

type probeAuthenticator struct{}

func (probeAuthenticator) Authenticate(_ context.Context, authorization string) (auth.Identity, error) {
	if authorization != "Bearer probe" {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	return auth.Identity{TenantID: "probe"}, nil
}

type probeStore struct {
	sandbox sandbox.Sandbox
}

func (s probeStore) CreateSandbox(context.Context, string, string, json.RawMessage, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errors.New("not implemented")
}
func (s probeStore) ListSandboxes(context.Context, string, int, int) ([]sandbox.Sandbox, int64, error) {
	return nil, 0, errors.New("not implemented")
}
func (s probeStore) GetSandbox(_ context.Context, tenantID, id string) (sandbox.Sandbox, error) {
	if tenantID != s.sandbox.TenantID || id != s.sandbox.ID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return s.sandbox, nil
}
func (s probeStore) UpdateSandbox(context.Context, string, string, sandbox.Patch) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errors.New("not implemented")
}
func (s probeStore) StartSandbox(context.Context, string, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errors.New("not implemented")
}
func (s probeStore) StopSandbox(context.Context, string, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errors.New("not implemented")
}
func (s probeStore) DeleteSandbox(context.Context, string, string) error {
	return errors.New("not implemented")
}

type chvAppLink struct {
	server *orchestrator.CHVServer
}

func (l chvAppLink) CreateSandboxAppLink(ctx context.Context, sandboxID string) (string, time.Time, error) {
	link, err := l.server.CreateSandboxAppLink(ctx, &nodev1.CreateSandboxAppLinkRequest{SandboxId: sandboxID, OwnershipGeneration: 1})
	if err != nil {
		return "", time.Time{}, err
	}
	if link.GetExpiresAt() == nil {
		return "", time.Time{}, errors.New("Node returned no app link expiry")
	}
	return link.GetUrl(), link.GetExpiresAt().AsTime(), nil
}

func probeAPIT3(ctx context.Context, binary, directory, digest string) (result apiT3ProbeResult, err error) {
	if _, err := templateartifact.VerifyArtifact(directory, digest); err != nil {
		return result, err
	}
	state, err := os.MkdirTemp("", "isodo-api-t3-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(state)
	server, err := orchestrator.NewCHVServer(orchestrator.NodeStatus{
		NodeID: "validation-node", BootID: uuid.NewString(),
		TotalVCPU: 4, AvailableVCPU: 4, TotalMemoryBytes: 8 << 30, AvailableMemoryBytes: 8 << 30,
	}, orchestrator.CHVConfig{
		Directory: state, Binary: binary, Bucket: "local",
		Fetcher: &localArtifactFetcher{directory: directory, digest: digest},
	})
	if err != nil {
		return result, err
	}
	defer server.Close()

	sandboxID := uuid.NewString()
	create := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID, RequestHash: probeRequestHash("probe-api-t3:" + sandboxID),
		OwnershipGeneration: 1, Revision: 1, Action: nodev1.OperationAction_OPERATION_ACTION_CREATE,
		TemplateId: uuid.NewString(), TemplateBuildId: uuid.NewString(),
		TemplateArtifact: &nodev1.TemplateArtifact{ManifestDigest: digest, Bucket: "local", Prefix: "templates/sha256/" + digest},
		Resources:        &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 2 << 30},
		Network:          &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_DENY},
	}
	started := time.Now()
	if _, err := server.ApplyOperation(ctx, create); err != nil {
		return result, err
	}
	if err := waitNodeOperation(ctx, server, create.OperationId); err != nil {
		return result, err
	}
	result.SandboxID = sandboxID
	result.CreateToReadyMS = time.Since(started).Milliseconds()

	publisher, err := preview.New(preview.Config{Secret: "probe-preview", Domain: "localhost", Scheme: "http", APIPort: "43123", Relay: chvRelayDialer{server: server}})
	if err != nil {
		return result, err
	}
	handler := sandbox.NewHandler(probeStore{sandbox: sandbox.Sandbox{
		ID: sandboxID, TenantID: "probe", Name: "probe", Metadata: json.RawMessage(`{}`),
		DesiredState: "running", ObservedState: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}, chvAppLink{server: server})
	handler.SetAppLinkPublisher(publisher)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: probeAuthenticator{}})
	gateway := publisher.Wrap(mux)

	linkStart := time.Now()
	linkRequest := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+sandboxID+"/app-link", nil)
	linkRequest.Header.Set("Authorization", "Bearer probe")
	linkResponse := httptest.NewRecorder()
	gateway.ServeHTTP(linkResponse, linkRequest)
	result.AppLinkStatus = linkResponse.Code
	result.AppLinkMS = time.Since(linkStart).Milliseconds()
	if linkResponse.Code != http.StatusOK {
		return result, fmt.Errorf("app-link status %d: %s", linkResponse.Code, linkResponse.Body.String())
	}
	var link publicapi.SandboxAppLink
	if err := json.Unmarshal(linkResponse.Body.Bytes(), &link); err != nil {
		return result, err
	}
	if link.Capabilities == nil {
		return result, errors.New("app-link returned no capabilities")
	}
	result.Capabilities = append([]string(nil), (*link.Capabilities)...)
	for _, capability := range result.Capabilities {
		if capability == "terminal" {
			result.TerminalAdvertised = true
		}
	}
	if !result.TerminalAdvertised {
		return result, errors.New("app-link did not advertise terminal capability")
	}
	result.PublicURL = link.Url
	result.RelayUsed = true
	pairing, err := url.Parse(link.PairingUrl)
	if err != nil {
		return result, err
	}
	authRequest := httptest.NewRequest(http.MethodGet, "http://"+pairing.Host+pairing.EscapedPath()+"?next=/pair", nil)
	authResponse := httptest.NewRecorder()
	gateway.ServeHTTP(authResponse, authRequest)
	if authResponse.Code != http.StatusFound || authResponse.Header().Get("Location") != "/pair" {
		return result, fmt.Errorf("preview auth status %d location %q", authResponse.Code, authResponse.Header().Get("Location"))
	}
	cookies := authResponse.Result().Cookies()
	if len(cookies) != 1 {
		return result, fmt.Errorf("preview auth set %d cookies", len(cookies))
	}
	appRequest := httptest.NewRequest(http.MethodGet, "http://"+pairing.Host+"/", nil)
	appRequest.AddCookie(cookies[0])
	appResponse := httptest.NewRecorder()
	gateway.ServeHTTP(appResponse, appRequest)
	result.AppHTTPStatus = appResponse.Code
	result.AppHTMLOK = appResponse.Code == http.StatusOK && strings.Contains(appResponse.Body.String(), `id="root"`)
	if !result.AppHTMLOK {
		return result, fmt.Errorf("preview T3 returned HTTP %d without the web root", appResponse.Code)
	}

	stop := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID, RequestHash: probeRequestHash("probe-api-stop:" + sandboxID),
		OwnershipGeneration: 1, Revision: 2, Action: nodev1.OperationAction_OPERATION_ACTION_STOP,
	}
	if _, err := server.ApplyOperation(ctx, stop); err != nil {
		return result, err
	}
	if err := waitNodeOperation(ctx, server, stop.OperationId); err != nil {
		return result, err
	}
	result.Stopped = true
	remove := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID, RequestHash: probeRequestHash("probe-api-delete:" + sandboxID),
		OwnershipGeneration: 1, Revision: 3, Action: nodev1.OperationAction_OPERATION_ACTION_DELETE,
	}
	if _, err := server.ApplyOperation(ctx, remove); err != nil {
		return result, err
	}
	if err := waitNodeOperation(ctx, server, remove.OperationId); err != nil {
		return result, err
	}
	result.Deleted = true
	return result, nil
}

type chvRelayDialer struct {
	server *orchestrator.CHVServer
}

func (d chvRelayDialer) RelayPort(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error) {
	return d.server.RelayPortConn(ctx, sandboxID, generation, port, service)
}
