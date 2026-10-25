package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/rpcsecurity"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type TLSFiles struct {
	Certificate string
	Key         string
	CA          string
	ServerName  string
	NodeID      string
}

type Client struct {
	connection *grpc.ClientConn
	service    nodev1.NodeOrchestratorClient
	nodeID     string
}

func NewClient(target string, files TLSFiles) (*Client, error) {
	if target == "" || files.NodeID == "" {
		return nil, errors.New("node gRPC target and expected node ID are required")
	}
	credentials, err := rpcsecurity.ClientCredentials(files.Certificate, files.Key, files.CA, files.ServerName)
	if err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(credentials))
	if err != nil {
		return nil, fmt.Errorf("create node gRPC connection: %w", err)
	}
	return &Client{
		connection: connection,
		service:    nodev1.NewNodeOrchestratorClient(connection),
		nodeID:     files.NodeID,
	}, nil
}

func (c *Client) GetNodeStatus(ctx context.Context) (*nodev1.NodeStatus, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	result, err := c.service.GetNodeStatus(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("get node status: %w", err)
	}
	if result.GetNodeId() != c.nodeID || result.GetBootId() == "" || result.GetProtocolMajor() != 1 {
		return nil, errors.New("node returned an incomplete or incompatible status")
	}
	return result, nil
}

func (c *Client) ApplyOperation(ctx context.Context, request *nodev1.ApplyOperationRequest) (*nodev1.ApplyOperationResponse, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if request == nil || request.OperationId == "" || request.RequestHash == "" {
		return nil, errors.New("node operation ID and request hash are required")
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	response, err := c.service.ApplyOperation(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("apply node operation: %w", err)
	}
	return response, nil
}

func (c *Client) GetOperation(ctx context.Context, request *nodev1.GetOperationRequest) (*nodev1.Operation, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if request == nil || request.OperationId == "" || request.RequestHash == "" {
		return nil, errors.New("node operation ID and request hash are required")
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	operation, err := c.service.GetOperation(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("get node operation: %w", err)
	}
	return operation, nil
}

func (c *Client) InspectSandbox(ctx context.Context, request *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if request == nil || request.SandboxId == "" || request.OwnershipGeneration == 0 {
		return nil, errors.New("node Sandbox ID and ownership generation are required")
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	inspection, err := c.service.InspectSandbox(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("inspect node Sandbox: %w", err)
	}
	return inspection, nil
}

// CreateSandboxAppLink asks the active Node generation to mint a short-lived,
// T3-native pairing URL. The Control API must authorize sandbox ownership
// before calling this method; the Node never receives a platform credential.
func (c *Client) CreateSandboxAppLink(ctx context.Context, sandboxID string) (string, time.Time, error) {
	if err := c.requireService(); err != nil {
		return "", time.Time{}, err
	}
	if sandboxID == "" {
		return "", time.Time{}, errors.New("sandbox ID is required")
	}
	if _, err := c.GetNodeStatus(ctx); err != nil {
		return "", time.Time{}, err
	}
	// T3 may still be finishing its boot-time start, so this call is allowed
	// more time than ordinary control RPCs.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
	}
	link, err := c.service.CreateSandboxAppLink(ctx, &nodev1.CreateSandboxAppLinkRequest{SandboxId: sandboxID})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create T3 pairing link: %w", err)
	}
	if link.GetUrl() == "" || link.GetExpiresAt() == nil || !link.GetExpiresAt().IsValid() {
		return "", time.Time{}, errors.New("Node returned an incomplete T3 pairing link")
	}
	return link.GetUrl(), link.GetExpiresAt().AsTime(), nil
}

func (c *Client) WatchOperation(ctx context.Context, request *nodev1.WatchOperationRequest) (nodev1.NodeOrchestrator_WatchOperationClient, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if request == nil || request.OperationId == "" {
		return nil, errors.New("node operation ID is required")
	}
	stream, err := c.service.WatchOperation(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("watch node operation: %w", err)
	}
	return stream, nil
}

func (c *Client) requireService() error {
	if c == nil || c.service == nil {
		return errors.New("node gRPC client is not initialized")
	}
	return nil
}

func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 10*time.Second)
}

func (c *Client) Close() error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.Close()
}
