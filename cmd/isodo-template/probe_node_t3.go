package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

type nodeT3ProbeResult struct {
	ArtifactVerificationMS int64  `json:"artifact_verification_ms"`
	RuntimeSnapshot        bool   `json:"runtime_snapshot"`
	SandboxID              string `json:"sandbox_id"`
	BootID                 string `json:"boot_id"`
	GuestEpoch             string `json:"guest_epoch"`
	CreateToReadyMS        int64  `json:"create_to_ready_ms"`
	CreateToAppLinkMS      int64  `json:"create_to_app_link_ms"`
	AppHTTPStatus          int    `json:"app_http_status"`
	AppHTMLOK              bool   `json:"app_html_ok"`
	PairingTokenIssued     bool   `json:"pairing_token_issued"`
	Restarted              bool   `json:"restarted"`
	RestartBootID          string `json:"restart_boot_id,omitempty"`
	RestartGuestEpoch      string `json:"restart_guest_epoch,omitempty"`
	RestartToAppLinkMS     int64  `json:"restart_to_app_link_ms,omitempty"`
	RestartHTTPStatus      int    `json:"restart_http_status,omitempty"`
	RestartHTMLOK          bool   `json:"restart_html_ok,omitempty"`
	Stopped                bool   `json:"stopped"`
	Deleted                bool   `json:"deleted"`
	AppURL                 string `json:"app_url"`
}

type localArtifactFetcher struct {
	directory string
	digest    string
	mu        sync.Mutex
	artifact  *templateartifact.Artifact
}

func (f *localArtifactFetcher) verify() (*templateartifact.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.artifact != nil {
		return f.artifact, nil
	}
	artifact, err := templateartifact.VerifyArtifact(f.directory, f.digest)
	if err != nil {
		return nil, err
	}
	f.artifact = artifact
	return artifact, nil
}

func (f *localArtifactFetcher) Fetch(context.Context, string, string) (string, error) {
	artifact, err := f.verify()
	if err != nil {
		return "", err
	}
	return artifact.Directory(), nil
}

func (f *localArtifactFetcher) FetchVerified(context.Context, string, string) (*templateartifact.Artifact, error) {
	return f.verify()
}

func probeNodeT3(ctx context.Context, binary, directory, digest, networkBridge string) (result nodeT3ProbeResult, err error) {
	verificationStarted := time.Now()
	verified, err := templateartifact.VerifyArtifact(directory, digest)
	if err != nil {
		return result, err
	}
	result.ArtifactVerificationMS = time.Since(verificationStarted).Milliseconds()
	result.RuntimeSnapshot = verified.Manifest().RuntimeSnapshot != nil
	state, err := os.MkdirTemp("", "isodo-node-t3-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(state)
	server, err := orchestrator.NewCHVServer(orchestrator.NodeStatus{
		NodeID: "validation-node", BootID: uuid.NewString(),
		TotalVCPU: 4, AvailableVCPU: 4, TotalMemoryBytes: 8 << 30, AvailableMemoryBytes: 8 << 30,
	}, orchestrator.CHVConfig{
		Directory: state, Binary: binary, Bucket: "local",
		Fetcher:       &localArtifactFetcher{directory: directory, digest: digest, artifact: verified},
		NetworkBridge: networkBridge,
	})
	if err != nil {
		return result, err
	}
	defer server.Close()

	sandboxID := uuid.NewString()
	operationID := uuid.NewString()
	request := &nodev1.ApplyOperationRequest{
		OperationId: operationID, SandboxId: sandboxID, RequestHash: probeRequestHash("probe-node-t3:" + sandboxID),
		OwnershipGeneration: 1, Revision: 1, Action: nodev1.OperationAction_OPERATION_ACTION_CREATE,
		TemplateId: uuid.NewString(), TemplateBuildId: uuid.NewString(),
		TemplateArtifact: &nodev1.TemplateArtifact{ManifestDigest: digest, Bucket: "local", Prefix: "templates/sha256/" + digest},
		Resources:        &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 2 << 30},
		Network:          probeNetwork(networkBridge),
	}
	started := time.Now()
	apply := func(input *nodev1.ApplyOperationRequest) error {
		_, applyErr := server.ApplyOperation(ctx, input)
		if applyErr != nil {
			return applyErr
		}
		return waitNodeOperation(ctx, server, input.OperationId)
	}
	if err := apply(request); err != nil {
		return result, err
	}
	creation, err := server.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: operationID, RequestHash: request.RequestHash})
	if err != nil {
		return result, err
	}
	result.SandboxID = sandboxID
	result.BootID = creation.BootId
	result.CreateToReadyMS = time.Since(started).Milliseconds()
	inspection, err := server.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: sandboxID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.GuestEpoch = inspection.GuestEpoch

	link, err := server.CreateSandboxAppLink(ctx, &nodev1.CreateSandboxAppLinkRequest{SandboxId: sandboxID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.CreateToAppLinkMS = time.Since(started).Milliseconds()
	result.AppURL = link.GetUrl()
	appURL, err := url.Parse(link.GetUrl())
	if err != nil {
		return result, err
	}
	if token := appURL.Fragment; strings.HasPrefix(token, "token=") && len(strings.TrimPrefix(token, "token=")) > 0 {
		result.PairingTokenIssued = true
	}
	if !result.PairingTokenIssued {
		return result, errors.New("app link did not carry a pairing token fragment")
	}
	result.AppHTTPStatus, result.AppHTMLOK, err = fetchT3App(appURL.Host)
	if err != nil {
		return result, err
	}
	if !result.AppHTMLOK {
		return result, fmt.Errorf("T3 app returned HTTP %d without the web root", result.AppHTTPStatus)
	}

	stop := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID,
		RequestHash:         probeRequestHash("probe-stop:" + sandboxID),
		OwnershipGeneration: 1, Revision: 2, Action: nodev1.OperationAction_OPERATION_ACTION_STOP,
	}
	if err := apply(stop); err != nil {
		return result, err
	}
	result.Stopped = true

	restart := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID,
		RequestHash:         probeRequestHash("probe-start:" + sandboxID),
		OwnershipGeneration: 1, Revision: 3, Action: nodev1.OperationAction_OPERATION_ACTION_START,
	}
	if err := apply(restart); err != nil {
		return result, err
	}
	result.Restarted = true
	startedOperation, err := server.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: restart.OperationId})
	if err != nil {
		return result, err
	}
	result.RestartBootID = startedOperation.GetBootId()
	if result.RestartBootID == "" || result.RestartBootID == result.BootID {
		return result, errors.New("restart did not allocate a new boot ID")
	}
	inspection, err = server.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: sandboxID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.RestartGuestEpoch = inspection.GetGuestEpoch()
	restartLinkStart := time.Now()
	restartLink, err := server.CreateSandboxAppLink(ctx, &nodev1.CreateSandboxAppLinkRequest{SandboxId: sandboxID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.RestartToAppLinkMS = time.Since(restartLinkStart).Milliseconds()
	restartURL, err := url.Parse(restartLink.GetUrl())
	if err != nil {
		return result, err
	}
	result.RestartHTTPStatus, result.RestartHTMLOK, err = fetchT3App(restartURL.Host)
	if err != nil {
		return result, err
	}
	if !result.RestartHTMLOK {
		return result, fmt.Errorf("restarted T3 app returned HTTP %d without the web root", result.RestartHTTPStatus)
	}
	stopAgain := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID,
		RequestHash:         probeRequestHash("probe-stop-again:" + sandboxID),
		OwnershipGeneration: 1, Revision: 4, Action: nodev1.OperationAction_OPERATION_ACTION_STOP,
	}
	if err := apply(stopAgain); err != nil {
		return result, err
	}
	deleted := &nodev1.ApplyOperationRequest{
		OperationId: uuid.NewString(), SandboxId: sandboxID,
		RequestHash:         probeRequestHash("probe-delete:" + sandboxID),
		OwnershipGeneration: 1, Revision: 5, Action: nodev1.OperationAction_OPERATION_ACTION_DELETE,
	}
	if err := apply(deleted); err != nil {
		return result, err
	}
	result.Deleted = true
	return result, nil
}

func probeRequestHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func waitNodeOperation(ctx context.Context, server *orchestrator.CHVServer, operationID string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := server.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: operationID})
		if err != nil {
			return err
		}
		switch operation.GetState() {
		case nodev1.OperationState_OPERATION_STATE_SUCCEEDED:
			return nil
		case nodev1.OperationState_OPERATION_STATE_FAILED:
			return fmt.Errorf("operation %s failed: %s %s", operationID, operation.GetErrorCode(), operation.GetMessage())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func fetchT3App(host string) (int, bool, error) {
	_, appPort, err := net.SplitHostPort(host)
	if err != nil {
		return 0, false, err
	}
	probeURL := "http://" + net.JoinHostPort("127.0.0.1", appPort) + "/"
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get(probeURL)
	if err != nil {
		return 0, false, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	_ = response.Body.Close()
	if readErr != nil {
		return 0, false, readErr
	}
	return response.StatusCode, response.StatusCode == http.StatusOK && strings.Contains(string(body), `id="root"`), nil
}

func probeNetwork(bridge string) *nodev1.NetworkSpec {
	if bridge == "" {
		return &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_DENY}
	}
	return &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_ALLOW}
}
