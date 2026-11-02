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
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/egressproxy"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/runtimeports"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
)

type egressBinding struct {
	identity            *guestv1.RuntimeIdentity
	workbenchConfigured bool
	cancel              context.CancelFunc
}

func (s *CHVServer) EnableInjections(manager *injection.Manager) {
	s.injections = manager
	manager.SetMCPHandler(s.serveMCP)
}

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
	if err := ctx.Err(); err != nil {
		return err
	}
	done := s.startEgressSetup(instance.Identity(), func(setupCtx context.Context) error {
		if err := s.configureCHVEgress(setupCtx, record, instance); err != nil {
			return err
		}
		return s.configureWorkbenchEgress(setupCtx, record, instance)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-done:
		return result.Err
	}
}

// Concurrent app-link requests share one boot's socket and Guest initialization.
// A caller leaving the page must not cancel setup for another waiting caller.
func (s *CHVServer) startEgressSetup(identity *guestv1.RuntimeIdentity, setup func(context.Context) error) <-chan singleflight.Result {
	key := fmt.Sprintf("%s/%d/%s/%s", identity.GetSandboxId(), identity.GetOwnershipGeneration(), identity.GetBootId(), identity.GetGuestEpoch())
	return s.egressSetup.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
		defer cancel()
		return nil, setup(ctx)
	})
}

func (s *CHVServer) configureCHVEgress(ctx context.Context, record sandboxRecord, instance CHVRuntime) error {
	identity := instance.Identity()
	s.mu.Lock()
	current, ok := s.journal.getSandbox(record.SandboxID)
	existing := s.egress[record.SandboxID]
	valid := ok && !s.closed && egressSetupMatches(record, current, identity, s.busy[record.SandboxID])
	s.mu.Unlock()
	if !valid {
		return errors.New("sandbox changed before configuring egress")
	}
	if existing != nil && proto.Equal(existing.identity, identity) {
		return nil
	}
	if err := s.installMCPConfig(ctx, record.SandboxID, instance, false); err != nil {
		return err
	}
	environment, err := s.chvProxyEnvironment(record.SandboxID)
	if err != nil {
		return err
	}
	contents, err := proxyEnvironmentFile(environment)
	if err != nil {
		return err
	}
	binding, err := startEgressBridge(s.ctx, fmt.Sprintf("%s_%d", instance.VsockSocket(), runtimeports.GuestEgress), s.injections.ProxyPort(record.SandboxID), func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.journal.getSandbox(record.SandboxID)
		return ok && !s.closed && !s.busy[record.SandboxID] && current.State == "running" && current.EgressAllowed && current.Generation == identity.GetOwnershipGeneration() && current.Revision == record.Revision && current.BootID == identity.GetBootId() && current.GuestEpoch == identity.GetGuestEpoch()
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
	if _, err := runT3GuestCommand(ctx, client, identity, "egress-ca-bundle-"+suffix, []string{"python3", "-c", chvCABundle}); err != nil {
		return fmt.Errorf("prepare Guest CA bundle: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok = s.journal.getSandbox(record.SandboxID)
	if !ok || s.closed || !egressSetupMatches(record, current, identity, s.busy[record.SandboxID]) {
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

// Boot preparation is owned by the lifecycle worker; callers cannot use the
// bridge until the journal is running and busy has been cleared.
func (s *CHVServer) prepareReadyEgress(ctx context.Context, record sandboxRecord, instance CHVRuntime) error {
	if !record.EgressAllowed || s.config.NetworkBridge != "" {
		return nil
	}
	if s.injections == nil {
		return errors.New("CHV egress proxy is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	setupCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return s.configureCHVEgress(setupCtx, record, instance)
}

func egressSetupMatches(expected, current sandboxRecord, identity *guestv1.RuntimeIdentity, busy bool) bool {
	if identity == nil || expected.SandboxID != current.SandboxID || identity.GetSandboxId() != expected.SandboxID || identity.GetGuestEpoch() == "" || !expected.EgressAllowed || !current.EgressAllowed || current.Generation != expected.Generation || identity.GetOwnershipGeneration() != expected.Generation || current.Revision != expected.Revision || current.BootID != expected.BootID || identity.GetBootId() != expected.BootID {
		return false
	}
	if expected.State == "creating" || expected.State == "starting" {
		return busy && current.State == expected.State && current.GuestEpoch == ""
	}
	return expected.State == "running" && current.State == "running" && !busy && current.GuestEpoch == identity.GetGuestEpoch()
}

func (s *CHVServer) chvProxyEnvironment(sandbox string) (map[string]string, error) {
	environment, err := s.injections.Environment(sandbox)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		parsed, err := url.Parse(environment[key])
		if err != nil {
			return nil, err
		}
		parsed.Host = fmt.Sprintf("127.0.0.1:%d", runtimeports.GuestEgress)
		environment[key] = parsed.String()
	}
	environment["NODE_USE_ENV_PROXY"] = "1"
	for _, key := range []string{"SSL_CERT_FILE", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO", "NPM_CONFIG_CAFILE"} {
		environment[key] = "/workspace/.isodo/ca-bundle.crt"
	}
	return environment, nil
}

func (s *CHVServer) configureWorkbenchEgress(ctx context.Context, record sandboxRecord, instance CHVRuntime) error {
	identity := instance.Identity()
	s.mu.Lock()
	binding := s.egress[record.SandboxID]
	current, ok := s.journal.getSandbox(record.SandboxID)
	valid := ok && !s.closed && egressSetupMatches(record, current, identity, s.busy[record.SandboxID]) && binding != nil && proto.Equal(binding.identity, identity)
	configured := binding != nil && binding.workbenchConfigured
	s.mu.Unlock()
	if !valid {
		return errors.New("sandbox changed before workbench proxy setup")
	}
	if configured {
		return nil
	}
	connection, err := guesttransport.NewVsockClientConn(instance.VsockSocket())
	if err != nil {
		return err
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)
	suffix := newT3RequestSuffix()
	// The template supervisor restarts T3. Never accept a caller-supplied PID or command.
	if _, err := runT3GuestCommand(ctx, client, identity, "egress-reload-"+suffix, []string{"/bin/sh", "-c", `python3 - <<'PY'
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
	current, ok = s.journal.getSandbox(record.SandboxID)
	if !ok || s.closed || !egressSetupMatches(record, current, identity, s.busy[record.SandboxID]) || s.egress[record.SandboxID] != binding {
		return errors.New("sandbox changed during workbench proxy setup")
	}
	binding.workbenchConfigured = true
	return nil
}

const chvCABundle = `import os,stat,secrets
root=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
d=os.open('.isodo',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=root)
f=os.open('injection-ca.crt',os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=d)
assert stat.S_ISREG(os.fstat(f).st_mode)
with os.fdopen(f,'rb') as stream: injected=stream.read(1048577)
assert len(injected)<=1048576
with open('/etc/ssl/certs/ca-certificates.crt','rb') as stream: trusted=stream.read(1048577)
assert len(trusted)<=1048576
name='.ca-bundle-'+secrets.token_hex(12)
f=os.open(name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o644,dir_fd=d)
try:
 with os.fdopen(f,'wb') as stream: stream.write(trusted+b'\n'+injected);stream.flush();os.fsync(stream.fileno())
 os.chown(name,1000,1000,dir_fd=d,follow_symlinks=False)
 os.replace(name,'ca-bundle.crt',src_dir_fd=d,dst_dir_fd=d);os.fsync(d)
finally:
 try:os.unlink(name,dir_fd=d)
 except FileNotFoundError:pass
`
