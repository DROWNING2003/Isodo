package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/egressproxy"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"google.golang.org/protobuf/proto"
)

type egressBinding struct {
	identity *guestv1.RuntimeIdentity
	cancel   context.CancelFunc
}

func (s *CHVServer) EnableInjections(manager *injection.Manager) { s.injections = manager }

// startEgressBridge accepts only this VM's guest-initiated vsock connections.
// Every connection rechecks the captured boot identity before dialing its proxy.
func startEgressBridge(parent context.Context, path string, port int, allow func() bool) (*egressBinding, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		_ = egressproxy.Serve(ctx, listener, func(ctx context.Context) (net.Conn, error) {
			if !allow() {
				return nil, errors.New("sandbox egress identity is no longer active")
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		})
	}()
	return &egressBinding{cancel: func() { cancel(); _ = listener.Close() }}, nil
}

func proxyEnvironmentFile(environment map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, key := range keys {
		if strings.ContainsAny(environment[key], "\r\n") {
			return nil, errors.New("invalid proxy environment value")
		}
		value, err := json.Marshal(environment[key])
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&output, "%s=%s\n", key, value)
	}
	return []byte(output.String()), nil
}

func (s *CHVServer) prepareCHVEgress(ctx context.Context, record sandboxRecord, instance CHVRuntime) error {
	if !record.EgressAllowed || s.config.NetworkBridge != "" {
		return nil
	}
	if s.injections == nil {
		return errors.New("CHV egress proxy is not configured")
	}
	identity := instance.Identity()
	s.mu.Lock()
	existing := s.egress[record.SandboxID]
	s.mu.Unlock()
	if existing != nil && proto.Equal(existing.identity, identity) {
		return nil
	}
	environment, err := s.injections.Environment(record.SandboxID)
	if err != nil {
		return err
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		parsed, err := url.Parse(environment[key])
		if err != nil {
			return err
		}
		parsed.Host = "127.0.0.1:1080"
		environment[key] = parsed.String()
	}
	environment["NODE_USE_ENV_PROXY"] = "1"
	for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO", "NPM_CONFIG_CAFILE"} {
		environment[key] = "/workspace/.isodo/ca-bundle.crt"
	}
	contents, err := proxyEnvironmentFile(environment)
	if err != nil {
		return err
	}
	binding, err := startEgressBridge(s.ctx, instance.VsockSocket()+"_1080", s.injections.ProxyPort(record.SandboxID), func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.journal.getSandbox(record.SandboxID)
		return ok && !s.closed && !s.busy[record.SandboxID] && current.State == "running" && current.EgressAllowed && current.Generation == identity.GetOwnershipGeneration() && current.BootID == identity.GetBootId() && current.GuestEpoch == identity.GetGuestEpoch()
	})
	if err != nil {
		return err
	}
	binding.identity = proto.Clone(identity).(*guestv1.RuntimeIdentity)
	installed := false
	defer func() {
		if !installed {
			binding.cancel()
		}
	}()
	connection, err := guesttransport.NewVsockClientConn(instance.VsockSocket())
	if err != nil {
		return err
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)
	suffix := newT3RequestSuffix()
	if err := writeGuestFile(ctx, client, identity, "egress-ca-"+suffix, "/workspace/.isodo/injection-ca.crt", s.injections.CACertPEM()); err != nil {
		return err
	}
	if err := writeGuestFile(ctx, client, identity, "egress-env-"+suffix, "/workspace/.isodo/proxy.env", contents); err != nil {
		return err
	}
	// The template supervisor restarts T3. Never accept a caller-supplied PID or command.
	if _, err := runT3GuestCommand(ctx, client, identity, "egress-reload-"+suffix, []string{"/bin/sh", "-c", `cat /etc/ssl/certs/ca-certificates.crt /workspace/.isodo/injection-ca.crt > /workspace/.isodo/ca-bundle.crt && python3 - <<'PY'
import os, signal, time
p=int(open("/workspace/.isodo/t3.pid").read())
c=open("/proc/%d/cmdline"%p,"rb").read().split(b"\0")
assert p>1 and c[0]==b"/usr/local/bin/node" and b"bin.mjs" in c and b"serve" in c
os.kill(p,signal.SIGTERM)
for _ in range(200):
    if not os.path.exists("/proc/%d"%p):
        break
    time.sleep(0.05)
else:
    raise RuntimeError("old T3 process did not exit")
PY`}); err != nil {
		return fmt.Errorf("configure T3 proxy; a template with the egress launcher is required: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.journal.getSandbox(record.SandboxID)
	if !ok || current.Generation != record.Generation || current.BootID != identity.GetBootId() || current.State != "running" || s.busy[record.SandboxID] {
		return errors.New("sandbox changed while configuring egress")
	}
	if s.egress == nil {
		s.egress = make(map[string]*egressBinding)
	}
	if old := s.egress[record.SandboxID]; old != nil {
		old.cancel()
	}
	s.egress[record.SandboxID] = binding
	installed = true
	return nil
}

func (s *CHVServer) closeEgressLocked(id string) {
	if binding := s.egress[id]; binding != nil {
		binding.cancel()
		delete(s.egress, id)
	}
}
