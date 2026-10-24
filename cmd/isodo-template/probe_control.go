package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	controlnode "github.com/DROWNING2003/Isodo/internal/control/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/control/postgres"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/control/sandboxworker"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/rpcsecurity"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type controlProbeResult struct {
	TemplateID                    string `json:"template_id"`
	BuildID                       string `json:"build_id"`
	ManifestDigest                string `json:"manifest_digest"`
	SandboxID                     string `json:"sandbox_id"`
	VerificationMilliseconds      int64  `json:"verification_ms"`
	CreateHTTPMilliseconds        int64  `json:"http_create_ms"`
	CreateToRunningMilliseconds   int64  `json:"create_to_running_ms"`
	CreateToFirstExecMilliseconds int64  `json:"create_to_first_exec_ms"`
	FixedCommandOK                bool   `json:"fixed_command_ok"`
	NodeDeleted                   bool   `json:"node_deleted"`
	HTTPDeleteMetadataOnly        bool   `json:"http_delete_metadata_only"`
	CapacityReleased              bool   `json:"capacity_released"`
}

// probeControl requires an empty, migrated validation database. It exercises the
// public handler and production queue/worker over mTLS; only the fixed command
// and Node deletion use operator access, since public Exec/delete are incomplete.
func probeControl(ctx context.Context, binary, cache, digest, templateID, buildID string) (result controlProbeResult, err error) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" || cache == "" {
		return result, errors.New("ISODO_TEST_DATABASE_URL and cache-dir are required")
	}
	store, err := postgres.New(ctx, databaseURL)
	if err != nil {
		return result, err
	}
	defer store.Close()
	bucket := os.Getenv("ISODO_TEMPLATE_S3_BUCKET")
	publication, err := store.RegisterSystemTemplate(ctx, "linux-base", "probe-control validation template", digest, bucket, "")
	if err != nil {
		return result, err
	}
	if (templateID != "" && templateID != publication.TemplateID) || (buildID != "" && buildID != publication.BuildID) {
		return result, errors.New("validation Registry IDs do not match requested IDs")
	}
	result.TemplateID, result.BuildID, result.ManifestDigest = publication.TemplateID, publication.BuildID, digest
	storage, err := templateartifact.NewS3Store(templateartifact.S3Config{Endpoint: os.Getenv("ISODO_TEMPLATE_S3_ENDPOINT"), Region: os.Getenv("ISODO_TEMPLATE_S3_REGION"), Bucket: bucket, AccessKey: os.Getenv("SEAWEEDFS_READ_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_READ_SECRET_KEY")})
	if err != nil {
		return result, err
	}
	verifyStart := time.Now()
	if _, err = storage.FetchVerified(ctx, cache, digest); err != nil {
		return result, err
	}
	result.VerificationMilliseconds = time.Since(verifyStart).Milliseconds()
	root, err := os.MkdirTemp(filepath.Dir(cache), ".control-probe-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	nodeID := "probe-control-node"
	var runtimeMu sync.Mutex
	var runtime *templatebuilder.Runtime
	server, err := orchestrator.NewCHVServer(orchestrator.NodeStatus{NodeID: nodeID, BootID: uuid.NewString(), TotalVCPU: 2, AvailableVCPU: 2, TotalMemoryBytes: 512 << 20, AvailableMemoryBytes: 512 << 20}, orchestrator.CHVConfig{Directory: root, CacheDirectory: cache, Binary: binary, Bucket: bucket, Fetcher: storage, Launch: func(create, lifetime context.Context, config templatebuilder.RuntimeConfig) (orchestrator.CHVRuntime, error) {
		instance, e := templatebuilder.StartRuntime(create, lifetime, config)
		if e == nil {
			runtimeMu.Lock()
			runtime = instance
			runtimeMu.Unlock()
		}
		return instance, e
	}})
	if err != nil {
		return result, err
	}
	defer server.Close()
	certs, err := newProbeTLS(root)
	if err != nil {
		return result, err
	}
	credentials, err := rpcsecurity.ServerCredentials(certs.ServerCert, certs.ServerKey, certs.CA, certs.ClientURI)
	if err != nil {
		return result, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return result, err
	}
	defer listener.Close()
	grpcServer := grpc.NewServer(grpc.Creds(credentials))
	nodev1.RegisterNodeOrchestratorServer(grpcServer, server)
	go grpcServer.Serve(listener)
	defer grpcServer.Stop()
	client, err := controlnode.NewClient(listener.Addr().String(), controlnode.TLSFiles{Certificate: certs.ClientCert, Key: certs.ClientKey, CA: certs.CA, ServerName: "probe-node", NodeID: nodeID})
	if err != nil {
		return result, err
	}
	defer client.Close()
	if _, err = client.GetNodeStatus(ctx); err != nil {
		return result, err
	}
	worker, err := sandboxworker.New(store, client, nodeID, "chv", slog.Default())
	if err != nil {
		return result, err
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(workerCtx) }()
	defer func() { cancelWorker(); <-workerDone }()
	tenant, err := store.ResolveTenant(ctx, "probe-control", uuid.NewString(), "probe operator")
	if err != nil {
		return result, err
	}
	key, err := createProbeKey(ctx, store, tenant)
	if err != nil {
		return result, err
	}
	httpServer := httptest.NewServer(httpapi.NewHandler(nil, auth.NewAPIKeyHandler(store), sandbox.NewHandler(store), &auth.Access{Keys: store}))
	defer httpServer.Close()
	started := time.Now()
	body, status, err := probeHTTP(ctx, http.MethodPost, httpServer.URL+"/v1/sandboxes", key, `{"name":"probe-control"}`)
	if err != nil {
		return result, err
	}
	if status != http.StatusCreated {
		return result, fmt.Errorf("HTTP create status %d: %s", status, body)
	}
	var created sandbox.Sandbox
	if err = json.Unmarshal(body, &created); err != nil {
		return result, err
	}
	if created.ID == "" {
		return result, errors.New("HTTP create returned no sandbox ID")
	}
	result.SandboxID = created.ID
	result.CreateHTTPMilliseconds = time.Since(started).Milliseconds()
	for {
		body, status, err = probeHTTP(ctx, http.MethodGet, httpServer.URL+"/v1/sandboxes/"+created.ID, key, "")
		if err != nil {
			return result, err
		}
		if status != http.StatusOK {
			return result, fmt.Errorf("HTTP poll status %d: %s", status, body)
		}
		var current sandbox.Sandbox
		if err = json.Unmarshal(body, &current); err != nil {
			return result, err
		}
		if current.ObservedState == "error" {
			return result, errors.New("Control reported sandbox creation failure")
		}
		if current.ObservedState == "running" {
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	result.CreateToRunningMilliseconds = time.Since(started).Milliseconds()
	runtimeMu.Lock()
	instance := runtime
	runtimeMu.Unlock()
	if instance == nil || instance.Identity().SandboxId != created.ID {
		return result, errors.New("running sandbox does not match captured runtime")
	}
	if _, err = templatebuilder.ProbeRuntimeCommand(ctx, instance, true); err != nil {
		return result, err
	}
	result.FixedCommandOK = true
	result.CreateToFirstExecMilliseconds = time.Since(started).Milliseconds()
	// Demonstrate the current public Delete boundary before operator cleanup.
	_, status, err = probeHTTP(ctx, http.MethodDelete, httpServer.URL+"/v1/sandboxes/"+created.ID, key, "")
	if err != nil {
		return result, err
	}
	if status != http.StatusNoContent {
		return result, fmt.Errorf("HTTP delete status %d", status)
	}
	inspection, err := client.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: created.ID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	if inspection.ObservedState != "running" {
		return result, errors.New("HTTP delete behavior changed; review validation boundary")
	}
	result.HTTPDeleteMetadataOnly = true
	request := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: created.ID, OwnershipGeneration: 1, Revision: 2, Action: nodev1.OperationAction_OPERATION_ACTION_DELETE}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(encoded)
	request.RequestHash = hex.EncodeToString(sum[:])
	if _, err = client.ApplyOperation(ctx, request); err != nil {
		return result, err
	}
	for {
		operation, e := client.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: request.OperationId, RequestHash: request.RequestHash})
		if e != nil {
			return result, e
		}
		if operation.State == nodev1.OperationState_OPERATION_STATE_FAILED {
			return result, errors.New("Node deletion failed")
		}
		if operation.State == nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	inspection, err = client.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: created.ID, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.NodeDeleted = inspection.ObservedState == "deleted"
	capacity, err := server.GetNodeStatus(ctx, new(emptypb.Empty))
	if err != nil {
		return result, err
	}
	result.CapacityReleased = capacity.AvailableVcpu == 2 && capacity.AvailableMemoryBytes == 512<<20
	if !result.NodeDeleted || !result.CapacityReleased {
		return result, errors.New("Node deletion did not release runtime capacity")
	}
	return result, nil
}

func createProbeKey(ctx context.Context, store *postgres.Store, tenant string) (string, error) {
	idBytes, secretBytes := make([]byte, 16), make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return "", err
	}
	id, secret := hex.EncodeToString(idBytes), base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	_, err := store.CreateAPIKey(ctx, tenant, auth.APIKey{ID: id, Name: "probe-control", Prefix: "iso_" + id, Scopes: []string{"sandboxes:read", "sandboxes:write"}}, hash[:])
	return "iso_" + id + "_" + secret, err
}

func probeHTTP(ctx context.Context, method, url, key, body string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("X-API-Key", key)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return data, response.StatusCode, err
}
