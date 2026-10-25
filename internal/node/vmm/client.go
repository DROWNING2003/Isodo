package vmm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const apiBaseURL = "http://cloud-hypervisor/api/v1"

type Client struct {
	httpClient *http.Client
}

type VMConfig struct {
	Disks   []DiskConfig  `json:"disks,omitempty"`
	CPUs    CPUConfig     `json:"cpus"`
	Memory  MemoryConfig  `json:"memory"`
	Payload PayloadConfig `json:"payload"`
	Vsock   VsockConfig   `json:"vsock"`
	Net     []NetConfig   `json:"net,omitempty"`
	Serial  ConsoleConfig `json:"serial"`
	Console ConsoleConfig `json:"console"`
}

type DiskConfig struct {
	Path         string `json:"path"`
	ImageType    string `json:"image_type"`
	BackingFiles bool   `json:"backing_files,omitzero"`
	ReadOnly     bool   `json:"readonly,omitempty"`
}

type CPUConfig struct {
	BootVCPUs uint32 `json:"boot_vcpus"`
	MaxVCPUs  uint32 `json:"max_vcpus"`
}

type MemoryConfig struct {
	Size uint64 `json:"size"`
}

type PayloadConfig struct {
	Kernel    string `json:"kernel"`
	Initramfs string `json:"initramfs"`
	Cmdline   string `json:"cmdline"`
}

type VsockConfig struct {
	CID    uint32 `json:"cid"`
	Socket string `json:"socket"`
}

type NetConfig struct {
	Tap       string `json:"tap"`
	Mac       string `json:"mac,omitempty"`
	NumQueues uint32 `json:"num_queues,omitempty"`
	QueueSize uint32 `json:"queue_size,omitempty"`
}

type ConsoleConfig struct {
	Mode string `json:"mode"`
	File string `json:"file,omitempty"`
}

type VMInfo struct {
	State string `json:"state"`
}

// MemoryRestoreMode selects how Cloud Hypervisor populates memory from a VM snapshot.
type MemoryRestoreMode string

const (
	// MemoryRestoreCopy eagerly copies the complete memory snapshot before resuming vCPUs.
	MemoryRestoreCopy MemoryRestoreMode = "Copy"
	// MemoryRestoreOnDemand uses userfaultfd to load guest pages on first access.
	MemoryRestoreOnDemand MemoryRestoreMode = "OnDemand"
	// MemoryRestoreCopyOnWrite maps snapshot memory privately and faults pages through the host page cache.
	MemoryRestoreCopyOnWrite MemoryRestoreMode = "CopyOnWrite"
)

type snapshotRequest struct {
	DestinationURL string `json:"destination_url"`
}

type restoreRequest struct {
	SourceURL         string            `json:"source_url"`
	MemoryRestoreMode MemoryRestoreMode `json:"memory_restore_mode"`
}

func NewClient(socketPath string) (*Client, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("Cloud Hypervisor API socket path is required")
	}
	transport := new(http.Transport)
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socketPath)
	}
	return &Client{httpClient: &http.Client{Transport: transport}}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/vmm.ping", nil, nil)
}

func (c *Client) Create(ctx context.Context, config VMConfig) error {
	if err := config.validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, "/vm.create", config, nil)
}

func (c *Client) Boot(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.boot", nil, nil)
}

// Pause leaves the VM paused so its memory and device state can be snapshotted consistently.
func (c *Client) Pause(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.pause", nil, nil)
}

// Resume resumes a paused VM after snapshotting or restore preparation.
func (c *Client) Resume(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.resume", nil, nil)
}

// Snapshot writes a paused VM's state and memory into a new, empty directory.
func (c *Client) Snapshot(ctx context.Context, destinationDir string) error {
	destinationURL, err := snapshotFileURL(destinationDir)
	if err != nil {
		return fmt.Errorf("validate Cloud Hypervisor snapshot destination: %w", err)
	}
	return c.do(ctx, http.MethodPut, "/vm.snapshot", snapshotRequest{DestinationURL: destinationURL}, nil)
}

// Restore loads a VM snapshot into a fresh Cloud Hypervisor instance using an explicit memory mode.
func (c *Client) Restore(ctx context.Context, sourceDir string, mode MemoryRestoreMode) error {
	if mode != MemoryRestoreCopy && mode != MemoryRestoreOnDemand && mode != MemoryRestoreCopyOnWrite {
		return fmt.Errorf("unsupported Cloud Hypervisor memory restore mode %q", mode)
	}
	sourceURL, err := snapshotFileURL(sourceDir)
	if err != nil {
		return fmt.Errorf("validate Cloud Hypervisor snapshot source: %w", err)
	}
	return c.do(ctx, http.MethodPut, "/vm.restore", restoreRequest{
		SourceURL:         sourceURL,
		MemoryRestoreMode: mode,
	}, nil)
}

func (c *Client) PowerButton(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.power-button", nil, nil)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.shutdown", nil, nil)
}

func (c *Client) Delete(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "/vm.delete", nil, nil)
}

func (c *Client) Info(ctx context.Context) (VMInfo, error) {
	var result VMInfo
	if err := c.do(ctx, http.MethodGet, "/vm.info", nil, &result); err != nil {
		return VMInfo{}, err
	}
	if result.State == "" {
		return VMInfo{}, errors.New("Cloud Hypervisor returned VM info without a state")
	}
	return result, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, result any) error {
	if c == nil || c.httpClient == nil {
		return errors.New("Cloud Hypervisor client is not initialized")
	}
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Cloud Hypervisor request: %w", err)
		}
		requestBody = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, apiBaseURL+path, requestBody)
	if err != nil {
		return fmt.Errorf("create Cloud Hypervisor request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Cloud Hypervisor request %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Cloud Hypervisor request %s returned HTTP %d: %q", path, response.StatusCode, strings.TrimSpace(string(details)))
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode Cloud Hypervisor response %s: %w", path, err)
	}
	return nil
}

func (c VMConfig) validate() error {
	for _, disk := range c.Disks {
		if disk.Path == "" || (disk.ImageType != "Raw" && disk.ImageType != "Qcow2") {
			return errors.New("Cloud Hypervisor disk path and explicit Raw or Qcow2 image type are required")
		}
	}
	if c.CPUs.BootVCPUs == 0 || c.CPUs.MaxVCPUs < c.CPUs.BootVCPUs {
		return errors.New("Cloud Hypervisor CPU configuration is invalid")
	}
	if c.Memory.Size == 0 || c.Payload.Kernel == "" || (c.Payload.Initramfs == "" && len(c.Disks) == 0) {
		return errors.New("Cloud Hypervisor memory and guest payload are required")
	}
	if c.Vsock.CID < 3 || c.Vsock.Socket == "" {
		return errors.New("Cloud Hypervisor Vsock CID and socket are required")
	}
	if c.Serial.Mode == "" || c.Console.Mode == "" {
		return errors.New("Cloud Hypervisor serial and console modes are required")
	}
	return nil
}

func snapshotFileURL(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("snapshot directory path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot directory: %w", err)
	}
	return (&url.URL{Scheme: "file", Path: absolutePath}).String(), nil
}

func (c *Client) Close() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}
