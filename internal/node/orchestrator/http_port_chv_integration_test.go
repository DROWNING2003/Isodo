package orchestrator

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	controlnode "github.com/DROWNING2003/Isodo/internal/control/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/rpcsecurity"
	"github.com/google/uuid"
	"golang.org/x/net/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Runs the public HTTP handler → Gateway → actual mTLS Node client/server →
// Guest PortForward → a loopback-only HTTP/WebSocket/SSE server inside a new VM.
// The account/lifecycle store is in memory; no production database is involved.
func TestRealCHVHTTPPortPreview(t *testing.T) {
	if os.Getenv("ISODO_CHV_INTEGRATION") != "1" {
		t.Skip("isolated Linux/KVM host required")
	}
	binary, kernel, initramfs := os.Getenv("ISODO_CHV_BINARY"), os.Getenv("ISODO_GUEST_KERNEL"), os.Getenv("ISODO_GUEST_INITRAMFS")
	for _, p := range []string{binary, kernel, initramfs} {
		if p == "" {
			t.Fatal("CHV binary, kernel and command-capable initramfs required")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp("/tmp", "isodo-port-chv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("CHV logs retained at %s", dir)
		} else {
			_ = os.RemoveAll(dir)
		}
	})
	logFile, err := os.Create(filepath.Join(dir, "vmm.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	socket := filepath.Join(dir, "api.sock")
	vsock := filepath.Join(dir, "vsock.sock")
	process := exec.Command(binary, "--api-socket", socket)
	process.Stdout = logFile
	process.Stderr = logFile
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() {
		_ = process.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	vm, err := vmm.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	for vm.Ping(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	node, input := commandNodeForSocket(vsock)
	id := input.SandboxID
	boot := func(revision uint64) {
		identity := &guestv1.RuntimeIdentity{SandboxId: id, OwnershipGeneration: 7, BootId: uuid.NewString(), GuestEpoch: uuid.NewString()}
		cfg := vmm.VMConfig{CPUs: vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1}, Memory: vmm.MemoryConfig{Size: 512 << 20},
			Payload: vmm.PayloadConfig{Kernel: kernel, Initramfs: initramfs, Cmdline: fmt.Sprintf("console=ttyS0 rdinit=/init panic=1 isodo.sandbox_id=%s isodo.ownership_generation=7 isodo.boot_id=%s isodo.guest_epoch=%s", id, identity.BootId, identity.GuestEpoch)},
			Vsock:   vmm.VsockConfig{CID: uint32(5000 + time.Now().UnixNano()%50000), Socket: vsock}, Serial: vmm.ConsoleConfig{Mode: "File", File: filepath.Join(dir, fmt.Sprintf("serial-%d.log", revision))}, Console: vmm.ConsoleConfig{Mode: "Off"}}
		if err := vm.Create(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		if err := vm.Boot(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := guesttransport.WaitReady(ctx, vsock, identity); err != nil {
			t.Fatal(err)
		}
		node.mu.Lock()
		node.live[id] = &commandRealRuntime{identity: identity, socket: vsock, vm: vm}
		node.journal.sandboxes[id] = sandboxRecord{SandboxID: id, State: "running", Generation: 7, Revision: revision, BootID: identity.BootId, GuestEpoch: identity.GuestEpoch}
		node.mu.Unlock()
	}
	boot(11)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	files, serverCreds := previewTestTLS(t)
	server := grpc.NewServer(grpc.Creds(serverCreds))
	nodev1.RegisterNodeOrchestratorServer(server, node)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := controlnode.NewClient(listener.Addr().String(), files)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	store := &previewCHVStore{item: sandbox.Sandbox{ID: id, TenantID: "owner", NodeID: files.NodeID, Revision: "11", OwnershipGeneration: 7, DesiredState: "running", ObservedState: "running"}}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(client)
	publisher, _ := preview.New(preview.Config{Secret: "isolated-test-only", Domain: "localhost", Scheme: "http", PublicPort: "18443"})
	handler.SetHTTPPreview(publisher, client)
	publisher.ConfigureHTTP(handler.AuthorizeHTTPPreview, client)
	gateway := httptest.NewServer(publisher.Wrap(httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: previewCHVAuth{}})))
	defer gateway.Close()
	httpClient := gateway.Client()
	httpClient.Timeout = 5 * time.Second
	request := func(method, path, host, token, body string) (*http.Response, error) {
		r, _ := http.NewRequestWithContext(ctx, method, gateway.URL+path, strings.NewReader(body))
		r.Host = host
		if token != "" {
			r.Header.Set("X-Isodo-Preview-Token", token)
		}
		return httpClient.Do(r)
	}
	issue := func(port int) preview.HTTPLink {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/sandboxes/"+id+"/previews", strings.NewReader(fmt.Sprintf(`{"port":%d}`, port)))
		r.Header.Set("Authorization", "Bearer owner")
		response, err := httpClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var link preview.HTTPLink
		if response.StatusCode != 201 {
			b, _ := io.ReadAll(response.Body)
			t.Fatalf("issue=%d %s", response.StatusCode, b)
		}
		if err := json.NewDecoder(response.Body).Decode(&link); err != nil {
			t.Fatal(err)
		}
		return link
	}
	// Wait for the loopback fixture; Guest readiness does not imply app readiness.
	waitApp := func(revision uint64) {
		for {
			record := node.journal.sandboxes[id]
			conn, e := node.relayPortTarget(ctx, &nodev1.RelayOpen{SandboxId: id, OwnershipGeneration: 7, Revision: revision, BootId: record.BootID, GuestEpoch: record.GuestEpoch, Service: "http-port", Port: 3000})
			if e == nil {
				_, e = io.WriteString(conn, "GET / HTTP/1.0\r\nHost: localhost\r\n\r\n")
				if e == nil {
					response, readErr := http.ReadResponse(bufio.NewReader(conn), nil)
					e = readErr
					if e == nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
				}
				conn.Close()
			}
			if e == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	waitApp(11)
	var old preview.HTTPLink
	t.Logf("[开始] 匿名 Public / Private 动态切换 Sandbox ID=%s", id)
	baseURL, _ := publisher.HTTPPortBaseURL(id, 3000)
	directURL, _ := url.Parse(baseURL)
	direct, err := request("GET", "/hello", directURL.Host, "", "")
	if err != nil {
		t.Fatal(err)
	}
	direct.Body.Close()
	if direct.StatusCode != 200 {
		t.Fatalf("direct default without issuance=%d", direct.StatusCode)
	}
	publicLink := issue(3000)
	if publicLink.Visibility != "public" || publicLink.AuthorizationURL != "" {
		t.Fatal("default is not anonymous public")
	}
	publicURL, _ := url.Parse(publicLink.URL)
	checkPublic := func(want int) {
		t.Helper()
		response, err := request("POST", "/hello", publicURL.Host, "", "anonymous")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("anonymous HTTP %d want %d", response.StatusCode, want)
		}
	}
	checkPublic(200)
	for _, port := range []int{1024, 1080, 8080, 31024, 31080, 38080} {
		response, err := request("GET", "/", fmt.Sprintf("%d-%s.localhost:18443", port, id), "", "")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("reserved %d HTTP %d", port, response.StatusCode)
		}
	}
	setVisibility := func(mode string) {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "PUT", gateway.URL+"/v1/sandboxes/"+id+"/previews/3000", strings.NewReader(`{"visibility":"`+mode+`"}`))
		r.Header.Set("Authorization", "Bearer owner")
		response, err := httpClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("visibility PUT=%d", response.StatusCode)
		}
	}
	setVisibility("private")
	checkPublic(401)
	privateLink := issue(3000)
	privateURL, _ := url.Parse(privateLink.AuthorizationURL)
	response, err := request("GET", "/hello", publicURL.Host, privateURL.Fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("private token=%d", response.StatusCode)
	}
	setVisibility("public")
	checkPublic(200)
	setVisibility("private")
	checkPublic(401)
	response, err = request("GET", "/hello", publicURL.Host, privateURL.Fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("old policy token survived toggle")
	}
	for _, port := range []uint16{5173, 8000} {
		if _, err := store.SetPortPreview(ctx, "owner", id, port, "private"); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("[成功] Public 默认匿名、Private 401、授权访问和双向切换 Sandbox ID=%s", id)
	for _, port := range []int{3000, 5173, 8000} {
		link := issue(port)
		old = link
		u, _ := url.Parse(link.AuthorizationURL)
		for _, method := range []string{"GET", "POST"} {
			response, err := request(method, "/hello?q=1", u.Host, u.Fragment, "payload")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || string(b) != "guest:"+method+":/hello?q=1:payload" {
				t.Fatalf("port=%d %s=%d %s", port, method, response.StatusCode, b)
			}
		}
	}
	link := issue(5173)
	u, _ := url.Parse(link.AuthorizationURL)
	response, err = request("GET", "/events", u.Host, u.Fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("data: tick\n\n"))
	if _, err := io.ReadFull(response.Body, data); err != nil || string(data) != "data: tick\n\n" {
		t.Fatal("SSE did not flush")
	}
	// WebSocket upgrade and frames on the same Vite-style port and Host.
	connection, err := net.Dial("tcp", strings.TrimPrefix(gateway.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	config, _ := websocket.NewConfig("ws://"+u.Host+"/hmr", "http://"+u.Host)
	config.Header.Set("X-Isodo-Preview-Token", u.Fragment)
	ws, err := websocket.NewClient(config, connection)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := websocket.Message.Send(ws, "update"); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := websocket.Message.Receive(ws, &text); err != nil || text != "hmr:update" {
		t.Fatalf("WebSocket=%q %v", text, err)
	}
	// Control intent invalidates active streams before the VM has stopped.
	store.mu.Lock()
	store.item.DesiredState = "stopped"
	store.mu.Unlock()
	start := time.Now()
	_, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if time.Since(start) > time.Second {
		t.Fatal("SSE survived stop intent")
	}
	_ = ws.SetReadDeadline(time.Now().Add(time.Second))
	if err := websocket.Message.Receive(ws, &text); err == nil {
		t.Fatal("WebSocket survived stop intent")
	}
	ws.Close()
	checkOld := func(link preview.HTTPLink) {
		t.Helper()
		u, _ := url.Parse(link.AuthorizationURL)
		r, err := request("GET", "/", u.Host, u.Fragment, "")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 403 {
			t.Fatalf("old grant status=%d", r.StatusCode)
		}
	}
	checkOld(old)
	node.mu.Lock()
	record := node.journal.sandboxes[id]
	record.State = "stopped"
	record.Revision = 12
	node.journal.sandboxes[id] = record
	node.mu.Unlock()
	if err := vm.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	boot(13)
	waitApp(13)
	store.mu.Lock()
	store.item.DesiredState = "running"
	store.item.Revision = "13"
	store.mu.Unlock()
	checkOld(old)
	fresh := issue(3000)
	freshURL, _ := url.Parse(fresh.AuthorizationURL)
	r, err := request("GET", "/", freshURL.Host, freshURL.Fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("new boot preview=%d", r.StatusCode)
	}
	// Node must also cut off a relay independently of the Control store.
	active, err := request("GET", "/events", freshURL.Host, freshURL.Fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(active.Body, data); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	node.busy[id] = true
	node.mu.Unlock()
	start = time.Now()
	_, _ = io.ReadAll(active.Body)
	active.Body.Close()
	if time.Since(start) > time.Second {
		t.Fatal("Node relay survived lifecycle admission")
	}
	node.mu.Lock()
	delete(node.busy, id)
	node.mu.Unlock()
	store.mu.Lock()
	store.item.Deletion = &sandbox.Deletion{}
	store.mu.Unlock()
	checkOld(fresh)
	if err := vm.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	delete(node.live, id)
	delete(node.journal.sandboxes, id)
	node.mu.Unlock()
	t.Log("real CHV: public grant, mTLS Node RelayPort, Guest loopback GET/POST on 3000/5173/8000, SSE flush, WebSocket frames, active stop revocation, VM restart and delete grant fencing passed")
}

type previewCHVStore struct {
	sandbox.Store
	mu       sync.Mutex
	item     sandbox.Sandbox
	policies map[uint16]preview.PortPolicy
}

func (s *previewCHVStore) GetSandbox(_ context.Context, tenant, id string) (sandbox.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenant != s.item.TenantID || id != s.item.ID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return s.item, nil
}

func (s *previewCHVStore) LookupPreviewSandbox(ctx context.Context, id string) (sandbox.Sandbox, error) {
	return s.GetSandbox(ctx, "owner", id)
}
func (s *previewCHVStore) GetPortPreview(ctx context.Context, tenant, id string, port uint16) (preview.PortPolicy, error) {
	if _, err := s.GetSandbox(ctx, tenant, id); err != nil {
		return preview.PortPolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policies[port]
	if !ok {
		return preview.PortPolicy{Visibility: "public"}, nil
	}
	return p, nil
}
func (s *previewCHVStore) SetPortPreview(ctx context.Context, tenant, id string, port uint16, visibility string) (preview.PortPolicy, error) {
	if _, err := s.GetSandbox(ctx, tenant, id); err != nil {
		return preview.PortPolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policies == nil {
		s.policies = make(map[uint16]preview.PortPolicy)
	}
	p := s.policies[port]
	if p.Visibility != visibility {
		p.Revision++
	}
	p.Visibility = visibility
	s.policies[port] = p
	return p, nil
}

type previewCHVAuth struct{}

func (previewCHVAuth) Authenticate(_ context.Context, header string) (auth.Identity, error) {
	if header != "Bearer owner" {
		return auth.Identity{}, errors.New("unauthenticated")
	}
	return auth.Identity{TenantID: "owner", Subject: "owner"}, nil
}

func previewTestTLS(t *testing.T) (controlnode.TLSFiles, credentials.TransportCredentials) {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := url.Parse("spiffe://isodo.test/control")
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated preview test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, DNSNames: []string{"preview-node.test"}, URIs: []*url.URL{identity}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	keyDER, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	creds, err := rpcsecurity.ServerCredentials(certPath, keyPath, certPath, identity.String())
	if err != nil {
		t.Fatal(err)
	}
	return controlnode.TLSFiles{Certificate: certPath, Key: keyPath, CA: certPath, ServerName: "preview-node.test", NodeID: "isolated-node"}, creds
}
