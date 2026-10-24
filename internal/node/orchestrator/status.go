package orchestrator

import (
	"context"
	"errors"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type NodeStatus struct {
	NodeID                 string
	BootID                 string
	OrchestratorVersion    string
	CloudHypervisorVersion string
	TotalVCPU              uint32
	AvailableVCPU          uint32
	TotalMemoryBytes       uint64
	AvailableMemoryBytes   uint64
	Capabilities           []string
	Draining               bool
}

type StatusServer struct {
	nodev1.UnimplementedNodeOrchestratorServer
	status NodeStatus
}

func NewStatusServer(nodeStatus NodeStatus) (*StatusServer, error) {
	if nodeStatus.NodeID == "" || nodeStatus.BootID == "" {
		return nil, errors.New("node ID and boot ID are required")
	}
	if nodeStatus.TotalVCPU == 0 || nodeStatus.TotalMemoryBytes == 0 {
		return nil, errors.New("node CPU and memory capacity are required")
	}
	if nodeStatus.AvailableVCPU > nodeStatus.TotalVCPU || nodeStatus.AvailableMemoryBytes > nodeStatus.TotalMemoryBytes {
		return nil, errors.New("available node capacity cannot exceed total capacity")
	}
	return &StatusServer{status: nodeStatus}, nil
}

func (s *StatusServer) GetNodeStatus(context.Context, *emptypb.Empty) (*nodev1.NodeStatus, error) {
	if s == nil {
		return nil, status.Error(codes.Internal, "node status is unavailable")
	}
	current := s.status
	return &nodev1.NodeStatus{
		NodeId:                 current.NodeID,
		BootId:                 current.BootID,
		ProtocolMajor:          1,
		ProtocolMinor:          0,
		OrchestratorVersion:    current.OrchestratorVersion,
		CloudHypervisorVersion: current.CloudHypervisorVersion,
		TotalVcpu:              current.TotalVCPU,
		AvailableVcpu:          current.AvailableVCPU,
		TotalMemoryBytes:       current.TotalMemoryBytes,
		AvailableMemoryBytes:   current.AvailableMemoryBytes,
		Capabilities:           append([]string(nil), current.Capabilities...),
		Draining:               current.Draining,
		ObservedAt:             timestamppb.Now(),
	}, nil
}
