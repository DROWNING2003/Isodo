package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
)

func TestCHVEgressBridgeInjectsAndRejectsStaleRuntime(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("credential was not injected outside Guest")
		}
		_, _ = io.WriteString(w, "model-result")
	}))
	defer upstream.Close()
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir(), HostGateway: "127.0.0.1", Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = manager.SetRules("sandbox", []injection.Rule{{Key: "one-off:model", Kind: injection.KindOneOff, Name: "model", Spec: injection.Spec{Type: injection.TypeHTTP, BaseURL: upstream.URL, HeaderName: "Authorization", IfHeaders: map[string]string{"Authorization": "Bearer placeholder"}}, Secret: injection.Secret{Headers: map[string]string{"Authorization": "Bearer test-secret"}}}})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := manager.Environment("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := url.Parse(environment["HTTPS_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/tmp", "isodo-egress-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "vsock_1080")
	var active atomic.Bool
	active.Store(true)
	bridge, err := startEgressBridge(t.Context(), path, manager.ProxyPort("sandbox"), active.Load)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.cancel()
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
	request.Header.Set("Authorization", "Bearer placeholder")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "model-result" || calls.Load() != 1 {
		t.Fatalf("response=%q calls=%d err=%v", body, calls.Load(), err)
	}
	active.Store(false)
	response, err = client.Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("stale runtime retained egress")
	}
	if calls.Load() != 1 {
		t.Fatal("stale runtime reached upstream")
	}
	bridge.cancel()
	if connection, err := net.Dial("unix", path); err == nil {
		connection.Close()
		t.Fatal("closed bridge still accepted Guest connections")
	}
}

func TestCHVAllowsNoNICOnlyWhenProxyIsConfigured(t *testing.T) {
	server := &CHVServer{config: CHVConfig{Bucket: "templates"}}
	request := createCHVRequest()
	request.Network.EgressMode = nodev1.EgressMode_EGRESS_MODE_ALLOW
	if err := server.validate(request); err == nil {
		t.Fatal("accepted egress without bridge or proxy")
	}
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	server.EnableInjections(manager)
	if err := server.validate(request); err != nil {
		t.Fatalf("no-NIC proxy rejected: %v", err)
	}
	if err := server.validate(createCHVRequest()); err != nil {
		t.Fatalf("deny rejected: %v", err)
	}
}
