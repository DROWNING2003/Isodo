package vmm

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestClientCreatesBootsAndInspectsVM(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "s")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := new(http.Server)
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/vmm.ping":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/vm.create":
			var config VMConfig
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if config.Payload.Kernel != "/templates/kernel" || config.Vsock.CID != 42 || len(config.Disks) != 1 || config.Disks[0].ImageType != "Raw" {
				http.Error(w, "unexpected VM config", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/vm.boot":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/vm.info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"Running"}`))
		default:
			http.NotFound(w, r)
		}
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(t.Context()); err != nil {
		t.Fatalf("ping VMM: %v", err)
	}
	config := VMConfig{
		Disks:  []DiskConfig{{Path: "/templates/rootfs.raw", ImageType: "Raw"}},
		CPUs:   CPUConfig{BootVCPUs: 2, MaxVCPUs: 2},
		Memory: MemoryConfig{Size: 512 << 20},
		Payload: PayloadConfig{
			Kernel:    "/templates/kernel",
			Initramfs: "/templates/initramfs",
			Cmdline:   "console=ttyS0 rdinit=/init",
		},
		Vsock:   VsockConfig{CID: 42, Socket: "/vms/sandbox/vsock.sock"},
		Serial:  ConsoleConfig{Mode: "Tty"},
		Console: ConsoleConfig{Mode: "Off"},
	}
	if err := client.Create(t.Context(), config); err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if err := client.Boot(t.Context()); err != nil {
		t.Fatalf("boot VM: %v", err)
	}
	info, err := client.Info(t.Context())
	if err != nil {
		t.Fatalf("inspect VM: %v", err)
	}
	if info.State != "Running" {
		t.Fatalf("VM state = %q, want Running", info.State)
	}
}

func TestClientRejectsUnexpectedSocketPath(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Fatal("NewClient accepted an empty socket path")
	}
}

func TestVMConfigRequiresExplicitDiskFormat(t *testing.T) {
	config := VMConfig{
		CPUs:    CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory:  MemoryConfig{Size: 512 << 20},
		Payload: PayloadConfig{Kernel: "/kernel"},
		Vsock:   VsockConfig{CID: 3, Socket: "/vsock"},
		Serial:  ConsoleConfig{Mode: "Off"}, Console: ConsoleConfig{Mode: "Off"},
		Disks: []DiskConfig{{Path: "/rootfs.raw"}},
	}
	if err := config.validate(); err == nil {
		t.Fatal("accepted disk without image type")
	}
	config.Disks[0].ImageType = "Raw"
	if err := config.validate(); err != nil {
		t.Fatalf("valid raw disk: %v", err)
	}
	config.Disks[0].ImageType = "raw"
	if err := config.validate(); err == nil {
		t.Fatal("accepted CLI spelling in JSON API config")
	}
}
