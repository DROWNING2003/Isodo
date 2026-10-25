package orchestrator

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	t3GuestPort       = 8080
	t3BundleGuestPath = "/workspace/.isodo/t3-server-dist.bundle"
	t3WorkspaceDir    = "/workspace/.isodo/t3"
	t3TemplateDir     = "/opt/isodo/t3"
	t3TemplateDigest  = "/opt/isodo/t3.bundle.sha256"
	t3WorkspaceDigest = "/workspace/.isodo/t3.bundle.sha256"
)

func (s *LimaServer) CreateSandboxAppLink(ctx context.Context, request *nodev1.CreateSandboxAppLinkRequest) (*nodev1.SandboxAppLink, error) {
	if request == nil || request.GetSandboxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID is required")
	}
	if s.t3Bundle == "" {
		return nil, status.Error(codes.FailedPrecondition, "T3 application is not enabled on this Node")
	}
	record, ok := s.journal.getSandbox(request.GetSandboxId())
	if !ok || (request.GetOwnershipGeneration() != 0 && record.Generation != request.GetOwnershipGeneration()) {
		return nil, status.Error(codes.NotFound, "Sandbox ownership was not found on this Node")
	}
	if record.State != "running" || record.BootID == "" || record.GuestEpoch == "" {
		return nil, status.Error(codes.FailedPrecondition, "Sandbox Guest is not running")
	}
	endpoint, err := s.manager.GuestEndpoint(request.GetSandboxId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "resolve Sandbox Guest endpoint: %v", err)
	}
	connection, err := guesttransport.NewTCPClientConn(endpoint)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "connect to Sandbox Guest: %v", err)
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)
	identity := &guestv1.RuntimeIdentity{
		SandboxId: request.GetSandboxId(), OwnershipGeneration: record.Generation,
		BootId: record.BootID, GuestEpoch: record.GuestEpoch,
	}
	// The app link is only useful while the application is actually serving;
	// restart it here if a previous process died (crash, Guest restart) so
	// opening a running sandbox cannot dead-end on a stale URL.
	if err := s.ensureT3Serving(ctx, record); err != nil {
		return nil, status.Errorf(codes.Unavailable, "start T3 application for Sandbox: %v", err)
	}
	baseURL, err := s.manager.SandboxAppURL(request.GetSandboxId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve Sandbox application URL: %v", err)
	}
	output, err := runT3GuestCommandStdout(ctx, client, identity, "t3-pair-"+newT3RequestSuffix(), []string{
		"/usr/local/bin/node", "/workspace/.isodo/t3/bin.mjs", "auth", "pairing", "create",
		"--base-dir", "/workspace/.t3", "--ttl", "5m", "--json",
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "create T3 pairing credential: %v", err)
	}
	var issued struct {
		Credential string `json:"credential"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(output), &issued); err != nil || issued.Credential == "" || issued.ExpiresAt == "" {
		return nil, status.Error(codes.Internal, "T3 pairing CLI returned an invalid credential response")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, issued.ExpiresAt)
	if err != nil {
		return nil, status.Error(codes.Internal, "T3 pairing CLI returned an invalid expiry time")
	}
	appURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, status.Error(codes.Internal, "T3 application URL is invalid")
	}
	appURL.Path = "/pair"
	appURL.RawQuery = ""
	appURL.Fragment = url.Values{"token": []string{issued.Credential}}.Encode()
	return &nodev1.SandboxAppLink{Url: appURL.String(), ExpiresAt: timestamppb.New(expiresAt)}, nil
}

// ensureT3Serving reports whether the application answers on its host port and
// starts it when it does not.
func (s *LimaServer) ensureT3Serving(ctx context.Context, record sandboxRecord) error {
	port, err := s.manager.SandboxAppHostPort(record.SandboxID)
	if err != nil {
		return err
	}
	// A T3 process started before secure connections were enabled does not
	// inherit the injection proxy environment. Restart it once when the CA
	// marker is missing or belongs to another CA.
	if s.injections != nil {
		if ok, err := s.t3InjectionReady(ctx, record); err != nil || !ok {
			if err := s.restartT3Process(ctx, record); err != nil {
				return err
			}
			// Force a fresh start instead of trusting a still-closing port: the
			// old process did not have the injection environment.
			return s.startT3Server(ctx, &nodev1.Operation{
				SandboxId:           record.SandboxID,
				OwnershipGeneration: record.Generation,
			}, record.BootID, record.GuestEpoch, true)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return nil
		}
	}
	return s.startT3Server(ctx, &nodev1.Operation{
		SandboxId:           record.SandboxID,
		OwnershipGeneration: record.Generation,
	}, record.BootID, record.GuestEpoch, true)
}

// Include the authenticated proxy URL: Node restarts rotate both its port and
// capability, even when the persisted CA remains unchanged.
func (s *LimaServer) injectionRuntimeMarker(sandboxID string) (string, error) {
	env, err := s.injections.Environment(sandboxID)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write(s.injections.CACertPEM())
	_, _ = digest.Write([]byte(env["HTTPS_PROXY"]))
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func (s *LimaServer) t3InjectionReady(ctx context.Context, record sandboxRecord) (bool, error) {
	client, identity, err := s.guestClient(record)
	if err != nil {
		return false, err
	}
	defer client.close()
	marker, err := s.injectionRuntimeMarker(record.SandboxID)
	if err != nil {
		return false, err
	}
	output, err := runT3GuestCommand(ctx, client.client, identity, "t3-injection-marker-"+newT3RequestSuffix(), []string{
		"/bin/cat", "/workspace/.isodo/injection-ca.sha256",
	})
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == marker, nil
}

func (s *LimaServer) restartT3Process(ctx context.Context, record sandboxRecord) error {
	client, identity, err := s.guestClient(record)
	if err != nil {
		return err
	}
	defer client.close()
	// T3 is the only process that serves bin.mjs; the marker check guarantees
	// this only runs when the existing process predates proxy configuration.
	_, err = runT3GuestCommand(ctx, client.client, identity, "t3-stop-"+newT3RequestSuffix(), []string{
		"/bin/sh", "-c", "pkill -f '[i]sodo-t3-supervisor' 2>/dev/null || true; pkill -f '[b]in.mjs serve' 2>/dev/null || true; sleep 1",
	})
	return err
}

func newT3RequestSuffix() string {
	var random [8]byte
	if _, err := io.ReadFull(cryptorand.Reader, random[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", random[:])
}

func (s *LimaServer) startT3Server(ctx context.Context, operation *nodev1.Operation, bootID, guestEpoch string, forceNewStart ...bool) error {
	if s.t3Bundle == "" {
		return nil
	}
	if operation == nil || operation.GetSandboxId() == "" || bootID == "" || guestEpoch == "" {
		return errors.New("sandbox operation and bound Guest identity are required to start T3")
	}
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           operation.GetSandboxId(),
		OwnershipGeneration: operation.GetOwnershipGeneration(),
		BootId:              bootID,
		GuestEpoch:          guestEpoch,
	}
	endpoint, err := s.manager.GuestEndpoint(operation.GetSandboxId())
	if err != nil {
		return err
	}
	connection, err := guesttransport.NewTCPClientConn(endpoint)
	if err != nil {
		return fmt.Errorf("connect to bound Lima Guest for T3 startup: %w", err)
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)

	bundle, err := os.Open(s.t3Bundle)
	if err != nil {
		return fmt.Errorf("open configured T3 server bundle: %w", err)
	}
	defer bundle.Close()
	info, err := bundle.Stat()
	if err != nil {
		return fmt.Errorf("inspect configured T3 server bundle: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return errors.New("configured T3 server bundle must be a non-empty regular file")
	}
	contentHash := sha256.New()
	if _, err := io.Copy(contentHash, bundle); err != nil {
		return fmt.Errorf("hash configured T3 server bundle: %w", err)
	}
	if _, err := bundle.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind configured T3 server bundle: %w", err)
	}
	digest := contentHash.Sum(nil)
	digestHex := fmt.Sprintf("%x", digest)
	bundlePath := t3BundleGuestPath
	if _, err := runT3GuestCommand(ctx, client, identity, "t3-prepare-"+digestHex[:16], []string{"/bin/mkdir", "-p", "/workspace/.isodo", "/workspace/.t3", "/workspace/.codex", "/workspace/.home"}); err != nil {
		return fmt.Errorf("prepare Lima Guest for T3: %w", err)
	}
	runtimeMarker := ""
	if s.injections != nil {
		runtimeMarker, err = s.injectionRuntimeMarker(operation.GetSandboxId())
		if err != nil {
			return err
		}
	}
	injectionCADigest := ""
	if s.injections != nil {
		caDigest := sha256.Sum256(s.injections.CACertPEM())
		injectionCADigest = fmt.Sprintf("%x", caDigest)
		if err := writeGuestFile(ctx, client, identity, "t3-injection-ca-"+injectionCADigest[:16], "/workspace/.isodo/injection-ca.crt", s.injections.CACertPEM()); err != nil {
			return fmt.Errorf("install injection CA in Lima Guest: %w", err)
		}
	}
	bundleReady, err := s.t3BundleReady(ctx, client, identity, digestHex)
	if err != nil {
		return fmt.Errorf("check T3 bundle in Lima Guest: %w", err)
	}
	if !bundleReady {
		if err := uploadT3Bundle(ctx, client, identity, "t3-upload-"+digestHex[:16], bundlePath, info.Size(), digest, bundle); err != nil {
			return fmt.Errorf("upload T3 server bundle to Lima Guest: %w", err)
		}
		if _, err := runT3GuestCommand(ctx, client, identity, "t3-extract-"+digestHex[:16], []string{
			"/bin/sh", "-c", t3PrepareScript, "isodo-t3-prepare", bundlePath, t3WorkspaceDir,
		}); err != nil {
			return fmt.Errorf("extract T3 server bundle in Lima Guest: %w", err)
		}
		if err := writeGuestFile(ctx, client, identity, "t3-bundle-digest-"+digestHex[:16], t3WorkspaceDigest, []byte(digestHex+"\n")); err != nil {
			return fmt.Errorf("record T3 bundle digest in Lima Guest: %w", err)
		}
	}

	requestID := "t3-start-" + digestHex[:16]
	if len(forceNewStart) > 0 && forceNewStart[0] {
		// The Guest deduplicates request IDs. A restart after killing the old
		// process must use a fresh execution ID, otherwise StartProcess returns
		// the retained, already-exited process.
		requestID += "-restart-" + newT3RequestSuffix()
	}
	env := map[string]string{
		"HOME":           "/workspace/.home",
		"T3CODE_HOME":    "/workspace/.t3",
		"CODEX_HOME":     "/workspace/.codex",
		"T3CODE_SANDBOX": "1",
		"PATH":           "/usr/local/bin:/usr/bin:/bin",
	}
	if s.injections != nil {
		proxyEnv, err := s.injections.Environment(operation.GetSandboxId())
		if err != nil {
			return fmt.Errorf("configure secure-connection proxy for T3: %w", err)
		}
		for name, value := range proxyEnv {
			env[name] = value
		}
	}
	process, err := startGuestProcessWithEnv(ctx, client, identity, requestID, []string{
		"/bin/sh", "-c", "cd /workspace/.isodo/t3 && while true; do /usr/local/bin/node bin.mjs serve --port 8080 --host 127.0.0.1 --base-dir /workspace/.t3 --mode web >>/workspace/.isodo/t3-server.log 2>&1; sleep 1; done # isodo-t3-supervisor",
	}, env)
	if err != nil {
		return fmt.Errorf("start T3 server in Lima Guest: %w", err)
	}
	port, err := s.manager.SandboxAppHostPort(operation.GetSandboxId())
	if err != nil {
		return err
	}
	endpointURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	httpClient := &http.Client{Timeout: 3 * time.Second}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
		if err != nil {
			return err
		}
		response, requestErr := httpClient.Do(request)
		if requestErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			closeErr := response.Body.Close()
			if readErr == nil && closeErr == nil && response.StatusCode == http.StatusOK && bytes.Contains(body, []byte(`id="root"`)) {
				if s.injections != nil {
					_ = writeGuestFile(ctx, client, identity, "t3-injection-marker-"+newT3RequestSuffix(), "/workspace/.isodo/injection-ca.sha256", []byte(runtimeMarker))
				}
				return nil
			}
			lastErr = fmt.Errorf("T3 endpoint returned status %d", response.StatusCode)
		} else {
			lastErr = requestErr
		}
		if processInfo, processErr := client.GetProcess(ctx, process.GetRef()); processErr == nil && processInfo.GetState() == guestv1.ProcessState_PROCESS_STATE_EXITED {
			logOutput, logErr := runT3GuestCommand(ctx, client, identity, "t3-log-"+digestHex[:16], []string{"/bin/cat", "/workspace/.isodo/t3-server.log"})
			return fmt.Errorf("T3 server exited before becoming ready (last endpoint error: %v, process exit: %v, log: %q, log read: %v)", lastErr, processInfo.GetResult(), logOutput, logErr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for T3 server readiness: %w (last error: %v)", ctx.Err(), lastErr)
		case <-deadline.C:
			return fmt.Errorf("T3 server did not become ready within two minutes (last error: %v)", lastErr)
		case <-ticker.C:
		}
	}
}

type guestClient struct {
	client guestv1.GuestServiceClient
	conn   *grpc.ClientConn
}

func (g guestClient) close() { _ = g.conn.Close() }

func (s *LimaServer) guestClient(record sandboxRecord) (guestClient, *guestv1.RuntimeIdentity, error) {
	endpoint, err := s.manager.GuestEndpoint(record.SandboxID)
	if err != nil {
		return guestClient{}, nil, err
	}
	connection, err := guesttransport.NewTCPClientConn(endpoint)
	if err != nil {
		return guestClient{}, nil, fmt.Errorf("connect to bound Lima Guest: %w", err)
	}
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           record.SandboxID,
		OwnershipGeneration: record.Generation,
		BootId:              record.BootID,
		GuestEpoch:          record.GuestEpoch,
	}
	return guestClient{client: guestv1.NewGuestServiceClient(connection), conn: connection}, identity, nil
}

// startGuestProcessWithEnv behaves like startGuestProcess for commands that need
// an explicit environment. T3 is a long-lived service: an explicit zero
// prevents the Guest default command deadline from killing its process group.
func startGuestProcessWithEnv(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string, env map[string]string) (*guestv1.ProcessInfo, error) {
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: requestID, Command: &guestv1.Command{Argv: argv}, Env: env, CommandTimeoutSeconds: new(uint32(0)),
	})
	if err == nil || !strings.Contains(status.Convert(err).Message(), "EXECUTION_EXPIRED") {
		return process, err
	}
	return client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: requestID + "-" + newT3RequestSuffix(), Command: &guestv1.Command{Argv: argv}, Env: env, CommandTimeoutSeconds: new(uint32(0)),
	})
}

// startGuestProcess starts a guest command, retrying once with a fresh request
// ID when the Guest no longer retains the deduplicated execution.
func startGuestProcess(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string) (*guestv1.ProcessInfo, error) {
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: requestID, Command: &guestv1.Command{Argv: argv},
	})
	if err == nil || !strings.Contains(status.Convert(err).Message(), "EXECUTION_EXPIRED") {
		return process, err
	}
	return client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: requestID + "-" + newT3RequestSuffix(), Command: &guestv1.Command{Argv: argv},
	})
}

func (s *LimaServer) t3BundleReady(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, digest string) (bool, error) {
	command := "set -eu; " +
		"[ -f '" + t3TemplateDigest + "' ] && [ \"$(cat '" + t3TemplateDigest + "')\" = '" + digest + "' ] && " +
		"[ -s '" + t3TemplateDir + "/bin.mjs' ] || exit 2; " +
		"if [ ! -f '" + t3WorkspaceDigest + "' ] || [ \"$(cat '" + t3WorkspaceDigest + "' 2>/dev/null || true)\" != '" + digest + "' ]; then " +
		"rm -rf '" + t3WorkspaceDir + "'; mkdir -p '" + t3WorkspaceDir + "'; " +
		"cp -a '" + t3TemplateDir + "/.' '" + t3WorkspaceDir + "/'; " +
		"printf '%s\\n' '" + digest + "' > '" + t3WorkspaceDigest + "'; fi; " +
		"[ -s '" + t3WorkspaceDir + "/bin.mjs' ]"
	_, err := runT3GuestCommand(ctx, client, identity, "t3-check-"+digest[:16], []string{"/bin/sh", "-c", command})
	return err == nil, nil
}

func writeGuestFile(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID, guestPath string, content []byte) error {
	digest := sha256.Sum256(content)
	return uploadT3Bundle(ctx, client, identity, requestID, guestPath, int64(len(content)), digest[:], bytes.NewReader(content))
}

func uploadT3Bundle(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID, guestPath string, size int64, digest []byte, source io.Reader) error {
	stream, err := client.WriteFile(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Header{Header: &guestv1.WriteFileHeader{
		Identity: identity, RequestId: requestID, Path: guestPath, SizeBytes: uint64(size), Sha256: digest, Overwrite: true,
	}}}); err != nil {
		return err
	}
	buffer := make([]byte, 64<<10)
	var offset uint64
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			chunk := bytes.Clone(buffer[:n])
			if err := stream.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Chunk{Chunk: &guestv1.FileChunk{Offset: offset, Data: chunk}}}); err != nil {
				return err
			}
			offset += uint64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}

func runT3GuestCommand(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string) (string, error) {
	return runT3GuestCommandWithOutputFilter(ctx, client, identity, requestID, argv, false)
}

func runT3GuestCommandStdout(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string) (string, error) {
	return runT3GuestCommandWithOutputFilter(ctx, client, identity, requestID, argv, true)
}

func runT3GuestCommandWithOutputFilter(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string, stdoutOnly bool) (string, error) {
	// Deduplication records expire; a deterministic request ID would then fail
	// forever. These commands are idempotent, so retry once under a new ID.
	process, err := startGuestProcess(ctx, client, identity, requestID, argv)
	if err != nil {
		return "", err
	}
	stream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return output.String(), errors.New("Guest command stream ended without an exit event")
		}
		if err != nil {
			return output.String(), err
		}
		if chunk := event.GetOutput(); chunk != nil && (!stdoutOnly || chunk.GetChannel() == guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT) {
			output.Write(chunk.GetData())
		}
		if exit := event.GetExit(); exit != nil {
			if exit.GetExitCode() != 0 {
				return output.String(), fmt.Errorf("Guest command exited with status %d", exit.GetExitCode())
			}
			return output.String(), nil
		}
	}
}
