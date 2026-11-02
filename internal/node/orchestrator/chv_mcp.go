package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/runtimeports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SyncMCP is reachable only on the existing mTLS runtime service. No OAuth
// credentials are accepted; Node only holds binding-scoped broker capabilities.
func (s *CHVServer) SyncMCP(ctx context.Context, request *nodev1.SyncMCPRequest) (*nodev1.SyncMCPResponse, error) {
	if request == nil || len(request.Bindings) > 64 {
		return nil, status.Error(codes.InvalidArgument, "too many MCP bindings")
	}
	s.mu.Lock()
	err := s.injectionTarget(request.Target)
	instance := s.live[request.GetTarget().GetSandboxId()]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, status.Error(codes.FailedPrecondition, "sandbox is not running")
	}
	bindings := []injection.MCPBinding{}
	seen := map[string]bool{}
	for _, b := range request.Bindings {
		if b == nil {
			return nil, status.Error(codes.InvalidArgument, "invalid MCP binding")
		}
		custom, ok := strings.CutPrefix(b.MarketMcpId, "custom-")
		validMarket := b.MarketMcpId == "github" || ok && canonicalUUID(custom)
		if !canonicalUUID(b.BindingId) || !validMarket || seen[b.MarketMcpId] || len(b.Capability) != 43 {
			return nil, status.Error(codes.InvalidArgument, "invalid MCP binding")
		}
		seen[b.MarketMcpId] = true
		bindings = append(bindings, injection.MCPBinding{ID: b.BindingId, MarketID: b.MarketMcpId, Capability: b.Capability})
	}
	guest, err := s.fileGuest(ctx, request.Target)
	if err != nil {
		return nil, err
	}
	defer guest.close()
	s.mu.Lock()
	err = s.injectionTarget(request.Target)
	current, _ := s.journal.getSandbox(request.Target.SandboxId)
	if err == nil && (!current.EgressAllowed || s.config.NetworkBridge != "") {
		err = status.Error(codes.FailedPrecondition, "MCP requires existing sandbox proxy egress")
	}
	if err == nil {
		err = s.injections.SetMCP(request.Target.SandboxId, bindings)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, status.Error(codes.Unavailable, "MCP binding could not be persisted")
	}
	if err = s.installMCPConfig(ctx, request.Target.SandboxId, instance, true); err != nil {
		return nil, status.Error(codes.Unavailable, "Codex config could not be merged; check TOML parser availability and managed section conflicts")
	}
	if err = guest.check(); err != nil {
		return nil, err
	}
	return &nodev1.SyncMCPResponse{RestartRequired: true}, nil
}
func (s *CHVServer) serveMCP(sandbox string, w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer isodo-mcp-placeholder" {
		http.Error(w, "MCP authorization required", 401)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "sessionless MCP accepts POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.injections == nil {
		http.Error(w, "MCP unavailable", 503)
		return
	}
	market := strings.TrimPrefix(r.URL.Path, "/isodo-mcp/")
	var b injection.MCPBinding
	for _, binding := range s.injections.MCP(sandbox) {
		if binding.MarketID == market {
			b = binding
			break
		}
	}
	if b.ID == "" {
		http.Error(w, "MCP is not bound", 403)
		return
	}
	s.mu.Lock()
	record, ok := s.journal.getSandbox(sandbox)
	instance := s.live[sandbox]
	s.mu.Unlock()
	if !ok || instance == nil {
		http.Error(w, "runtime unavailable", 409)
		return
	}
	identity := instance.Identity()
	target := &nodev1.SandboxTarget{SandboxId: sandbox, OwnershipGeneration: record.Generation, Revision: record.Revision}
	guard, err := s.fileGuest(r.Context(), target)
	if err != nil {
		http.Error(w, "runtime identity changed", 409)
		return
	}
	defer guard.close()
	check := func(ctx context.Context) error {
		if err := guard.check(); err != nil {
			return err
		}
		current := instance.Identity()
		if current.GetBootId() != identity.GetBootId() || current.GetGuestEpoch() != identity.GetGuestEpoch() {
			return status.Error(codes.Aborted, "Guest boot changed")
		}
		running, err := instance.Running(ctx)
		if err != nil || !running {
			return status.Error(codes.FailedPrecondition, "Guest not running")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	if check(ctx) != nil {
		http.Error(w, "runtime identity changed", 409)
		return
	}
	// This origin is trusted deployment config, never a Guest-provided URL. TLS
	// verification is mandatory and redirects are disabled for capability safety.
	origin := os.Getenv("ISODO_MCP_BROKER_URL")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		http.Error(w, "MCP broker is not configured", 503)
		return
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(origin, "/")+"/internal/mcp/"+b.ID, http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "MCP request failed", 400)
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.Capability)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Isodo-Sandbox", sandbox)
	req.Header.Set("X-Isodo-Generation", strconv.FormatUint(record.Generation, 10))
	req.Header.Set("X-Isodo-Revision", strconv.FormatUint(record.Revision, 10))
	req.Header.Set("X-Isodo-Node", s.status.status.NodeID)
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if check(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	client := http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 55 * time.Second, TLSHandshakeTimeout: 10 * time.Second}}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "MCP broker unavailable", 502)
		return
	}
	defer resp.Body.Close()
	defer client.CloseIdleConnections()
	if check(ctx) != nil {
		http.Error(w, "runtime identity changed", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}
func (s *CHVServer) installMCPConfig(ctx context.Context, sandbox string, instance CHVRuntime, force bool) error {
	bindings := s.injections.MCP(sandbox)
	if len(bindings) == 0 && !force {
		return nil
	}
	// Don't touch existing Agent configs when this feature has never been used.

	connection, err := guesttransport.NewVsockClientConn(instance.VsockSocket())
	if err != nil {
		return err
	}
	defer connection.Close()
	ids := make([]string, 0, len(bindings))
	for _, b := range bindings {
		ids = append(ids, b.MarketID)
	}
	data, _ := json.Marshal(ids)
	_, err = runT3GuestCommand(ctx, guestv1.NewGuestServiceClient(connection), instance.Identity(), "mcp-config-"+newT3RequestSuffix(), []string{"python3", "-c", codexMCPMerge, strconv.Itoa(runtimeports.GuestEgress), string(data)})
	return err
}

// Python's standard TOML parser validates the full document before and after
// merging an owned section. openat/O_NOFOLLOW and a per-directory lock prevent
// symlink traversal. Unknown/manual conflicting sections fail closed.
const codexMCPMerge = `import os,sys,stat,fcntl,secrets,json,re
try: import tomllib
except ModuleNotFoundError: import tomli as tomllib
root=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
try:
 os.mkdir('.codex',0o700,dir_fd=root)
 os.chown('.codex',1000,1000,dir_fd=root,follow_symlinks=False)
except FileExistsError: pass
d=os.open('.codex',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=root)
l=os.open('.isodo-mcp.lock',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600,dir_fd=d)
fcntl.flock(l,fcntl.LOCK_EX)
try:
 f=os.open('config.toml',os.O_RDONLY|os.O_NOFOLLOW,dir_fd=d)
 assert stat.S_ISREG(os.fstat(f).st_mode)
 with os.fdopen(f) as stream: old=stream.read(1048577)
except FileNotFoundError: old=''
assert len(old)<=1048576
parsed=tomllib.loads(old)
ids=['github'] if sys.argv[2]=='true' else [] if sys.argv[2]=='false' else json.loads(sys.argv[2])
assert isinstance(ids,list) and len(ids)<=64 and len(set(ids))==len(ids)
assert all(isinstance(x,str) and (x=='github' or re.fullmatch(r'custom-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}',x)) for x in ids)
active=bool(ids)
start='# BEGIN ISODO MANAGED GITHUB MCP\n';end='# END ISODO MANAGED GITHUB MCP\n'
assert old.count(start)==old.count(end) and old.count(start)<=1
if start in old:
 a=old.index(start);b=old.index(end,a)+len(end);base=old[:a]+old[b:]
else:
 if not active: sys.exit(0)
 assert 'isodo_github' not in parsed.get('mcp_servers',{}),'manual isodo_github configuration conflicts'
 base=old
section='\n'+start
base_parsed=tomllib.loads(base)
for identifier in sorted(ids):
 name='isodo_'+identifier.replace('-','_')
 assert name not in base_parsed.get('mcp_servers',{}),'manual MCP configuration conflicts'
 section+='[mcp_servers.'+name+']\nurl = "http://127.0.0.1:'+sys.argv[1]+'/isodo-mcp/'+identifier+'"\nhttp_headers = { Authorization = "Bearer isodo-mcp-placeholder" }\n'
 if identifier=='github': section+='enabled_tools = ["get_me", "get_file_contents", "search_repositories", "search_code", "list_branches", "list_commits", "get_commit", "list_issues", "issue_read", "search_issues", "list_pull_requests", "pull_request_read", "search_pull_requests"]\n'
section+=end
new=base.rstrip()+'\n'+(section if active else '')
tomllib.loads(new)
if new==old: sys.exit(0)
name='.config-mcp-'+secrets.token_hex(12)
f=os.open(name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=d)
with os.fdopen(f,'w') as stream: stream.write(new);stream.flush();os.fsync(stream.fileno())
os.chown(name,1000,1000,dir_fd=d,follow_symlinks=False)
if old:
 backup='.config-before-mcp-'+secrets.token_hex(12)
 bf=os.open(backup,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=d)
 with os.fdopen(bf,'w') as stream: stream.write(old);stream.flush();os.fsync(stream.fileno())
 os.chown(backup,1000,1000,dir_fd=d,follow_symlinks=False)
os.replace(name,'config.toml',src_dir_fd=d,dst_dir_fd=d)
os.fsync(d)
`
