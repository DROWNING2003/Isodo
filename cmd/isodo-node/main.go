package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/lima"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/rpcsecurity"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("isodo-node stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	nodeStatus, err := loadNodeStatus()
	if err != nil {
		return err
	}
	credentials, err := rpcsecurity.ServerCredentials(
		os.Getenv("ISODO_NODE_TLS_CERT"),
		os.Getenv("ISODO_NODE_TLS_KEY"),
		os.Getenv("ISODO_NODE_CLIENT_CA"),
		os.Getenv("ISODO_CONTROL_CLIENT_URI"),
	)
	if err != nil {
		return err
	}
	service, err := newNodeService(nodeStatus)
	if err != nil {
		return err
	}
	if closer, ok := service.(interface{ Close() }); ok {
		defer closer.Close()
	}
	listenAddress := os.Getenv("ISODO_NODE_LISTEN_ADDR")
	if listenAddress == "" {
		listenAddress = "0.0.0.0:9090"
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for Control RPC: %w", err)
	}
	server := grpc.NewServer(
		grpc.Creds(credentials),
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxSendMsgSize(1<<20),
	)
	nodev1.RegisterNodeOrchestratorServer(server, service)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	logger.Info("isodo-node listening", "node_id", nodeStatus.NodeID, "address", listenAddress)

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	case <-shutdownCtx.Done():
		server.GracefulStop()
		return nil
	}
}

func loadNodeStatus() (orchestrator.NodeStatus, error) {
	nodeID := os.Getenv("ISODO_NODE_ID")
	if nodeID == "" {
		return orchestrator.NodeStatus{}, errors.New("ISODO_NODE_ID is required")
	}
	var bootID string
	var totalMemory, availableMemory uint64
	var err error
	switch runtime.GOOS {
	case "linux":
		bootIDBytes, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return orchestrator.NodeStatus{}, fmt.Errorf("read Linux boot ID: %w", err)
		}
		bootID = strings.TrimSpace(string(bootIDBytes))
		totalMemory, availableMemory, err = linuxMemory()
	case "darwin":
		var bootIDBytes [16]byte
		if _, err := cryptorand.Read(bootIDBytes[:]); err != nil {
			return orchestrator.NodeStatus{}, fmt.Errorf("generate local Node boot ID: %w", err)
		}
		bootID = hex.EncodeToString(bootIDBytes[:])
		totalMemory, availableMemory, err = macOSMemory()
	default:
		return orchestrator.NodeStatus{}, fmt.Errorf("Node host metrics are unsupported on %s", runtime.GOOS)
	}
	if err != nil {
		return orchestrator.NodeStatus{}, err
	}
	capabilities := []string{"node.status.v1"}
	if nodeBackend() == "lima" {
		capabilities = append(capabilities, "sandbox.lifecycle.v1", "sandbox.provider.lima.v1")
		if os.Getenv("ISODO_T3_SERVER_BUNDLE") != "" {
			capabilities = append(capabilities, "sandbox.application.t3.v1")
		}
	}
	return orchestrator.NodeStatus{
		NodeID:                 nodeID,
		BootID:                 bootID,
		OrchestratorVersion:    envOr("ISODO_NODE_VERSION", "0.1.0-dev"),
		CloudHypervisorVersion: os.Getenv("ISODO_CH_VERSION"),
		TotalVCPU:              uint32(runtime.NumCPU()),
		AvailableVCPU:          uint32(runtime.NumCPU()),
		TotalMemoryBytes:       totalMemory,
		AvailableMemoryBytes:   availableMemory,
		Capabilities:           capabilities,
		Draining:               os.Getenv("ISODO_NODE_DRAINING") == "true",
	}, nil
}

func newNodeService(nodeStatus orchestrator.NodeStatus) (nodev1.NodeOrchestratorServer, error) {
	switch backend := nodeBackend(); backend {
	case "chv":
		if runtime.GOOS != "linux" {
			return nil, errors.New("CHV Node requires Linux/KVM")
		}
		storage, err := templateartifact.NewS3Store(templateartifact.S3Config{
			Endpoint: os.Getenv("ISODO_TEMPLATE_S3_ENDPOINT"), Region: os.Getenv("ISODO_TEMPLATE_S3_REGION"), Bucket: os.Getenv("ISODO_TEMPLATE_S3_BUCKET"),
			AccessKey: os.Getenv("SEAWEEDFS_READ_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_READ_SECRET_KEY"),
		})
		if err != nil {
			return nil, err
		}
		return orchestrator.NewCHVServer(nodeStatus, orchestrator.CHVConfig{
			Directory: envOr("ISODO_NODE_STATE_DIR", "/var/lib/isodo/node"), Binary: envOr("ISODO_CH_BINARY", "cloud-hypervisor"),
			Bucket: os.Getenv("ISODO_TEMPLATE_S3_BUCKET"), Fetcher: storage,
		})
	case "lima":
		cpus, err := intEnv("ISODO_LIMA_CPUS", 2)
		if err != nil {
			return nil, err
		}
		memoryGiB, err := intEnv("ISODO_LIMA_MEMORY_GIB", 4)
		if err != nil {
			return nil, err
		}
		diskGiB, err := intEnv("ISODO_LIMA_DISK_GIB", 20)
		if err != nil {
			return nil, err
		}
		t3Bundle := os.Getenv("ISODO_T3_SERVER_BUNDLE")
		manager, err := lima.NewManager(lima.Config{
			Binary:           os.Getenv("ISODO_LIMA_BINARY"),
			InstancePrefix:   os.Getenv("ISODO_LIMA_INSTANCE_PREFIX"),
			Template:         os.Getenv("ISODO_LIMA_TEMPLATE"),
			GuestAgentBinary: os.Getenv("ISODO_GUEST_AGENT_BINARY"),
			EnableT3Runtime:  t3Bundle != "",
			VMType:           os.Getenv("ISODO_LIMA_VM_TYPE"),
			CPUs:             cpus,
			MemoryGiB:        memoryGiB,
			DiskGiB:          diskGiB,
		})
		if err != nil {
			return nil, err
		}
		stateDir := os.Getenv("ISODO_NODE_STATE_DIR")
		if stateDir == "" {
			configDir, err := os.UserConfigDir()
			if err != nil {
				return nil, fmt.Errorf("resolve local Node state directory: %w", err)
			}
			stateDir = filepath.Join(configDir, "Isodo", "node")
		}
		return orchestrator.NewLimaServer(
			nodeStatus,
			manager,
			filepath.Join(stateDir, "journal.json"),
			t3Bundle,
		)
	default:
		return nil, fmt.Errorf("unsupported ISODO_NODE_BACKEND %q (expected chv or lima)", backend)
	}
}

func nodeBackend() string {
	return strings.ToLower(envOr("ISODO_NODE_BACKEND", "chv"))
}

func intEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func macOSMemory() (uint64, uint64, error) {
	readSysctl := func(name string) (uint64, error) {
		output, err := exec.Command("sysctl", "-n", name).Output()
		if err != nil {
			return 0, fmt.Errorf("read macOS %s: %w", name, err)
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse macOS %s: %w", name, err)
		}
		return value, nil
	}
	total, err := readSysctl("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := readSysctl("hw.pagesize")
	if err != nil {
		return 0, 0, err
	}
	output, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("read macOS available memory: %w", err)
	}
	var availablePages uint64
	for line := range strings.SplitSeq(string(output), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages free", "Pages inactive", "Pages speculative", "Pages purgeable":
			pages, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), "."), 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("parse macOS memory statistic %q: %w", key, err)
			}
			availablePages += pages
		}
	}
	return total, min(total, availablePages*pageSize), nil
}

func linuxMemory() (uint64, uint64, error) {
	contents, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("read Linux memory information: %w", err)
	}
	var total, available uint64
	for line := range strings.SplitSeq(string(contents), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = kilobytes * 1024
		case "MemAvailable":
			available = kilobytes * 1024
		}
	}
	if total == 0 || available == 0 {
		return 0, 0, errors.New("Linux memory information is incomplete")
	}
	return total, available, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
