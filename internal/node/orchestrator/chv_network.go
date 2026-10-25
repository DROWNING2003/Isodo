package orchestrator

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	defaultNetworkBridge = "isodo0"
	networkGateway       = "10.77.0.1"
)

// ensureSandboxNetwork allocates a stable TAP and guest address for a Sandbox
// and attaches the TAP to the configured host bridge. The caller must hold s.mu.
func (s *CHVServer) ensureSandboxNetwork(record *sandboxRecord) error {
	if s.config.NetworkBridge == "" {
		return nil
	}
	if record.Tap == "" {
		record.Tap = sandboxTapName(record.SandboxID)
	}
	if record.MAC == "" {
		record.MAC = sandboxMAC(record.SandboxID)
	}
	if record.GuestIP == "" {
		address, err := s.allocateGuestIPLocked(record.SandboxID)
		if err != nil {
			return err
		}
		record.GuestIP = address
	}
	record.Gateway = networkGateway
	if err := ensureTap(record.Tap, s.config.NetworkBridge); err != nil {
		return err
	}
	return nil
}

func (s *CHVServer) allocateGuestIPLocked(sandboxID string) (string, error) {
	if address, ok := s.networkIPs[sandboxID]; ok {
		return address, nil
	}
	used := make(map[string]struct{}, len(s.networkIPs))
	for _, address := range s.networkIPs {
		used[address] = struct{}{}
	}
	for host := uint32(2); host < 255; host++ {
		address := fmt.Sprintf("10.77.0.%d", host)
		if _, exists := used[address]; !exists {
			s.networkIPs[sandboxID] = address
			return address, nil
		}
	}
	return "", errors.New("CHV network address pool is exhausted")
}

func (s *CHVServer) removeSandboxNetwork(record sandboxRecord) {
	if record.Tap == "" {
		return
	}
	delete(s.networkIPs, record.SandboxID)
	_ = exec.Command("ip", "link", "del", record.Tap).Run()
}

func sandboxTapName(sandboxID string) string {
	compact := strings.ReplaceAll(sandboxID, "-", "")
	if len(compact) > 8 {
		compact = compact[:8]
	}
	return "isodo-" + compact
}

func sandboxMAC(sandboxID string) string {
	sum := sha256.Sum256([]byte(sandboxID))
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", sum[0], sum[1], sum[2])
}

func ensureTap(tap, bridge string) error {
	if err := exec.Command("ip", "link", "show", tap).Run(); err != nil {
		if output, addErr := exec.Command("ip", "tuntap", "add", "dev", tap, "mode", "tap").CombinedOutput(); addErr != nil {
			return fmt.Errorf("create CHV TAP %s: %w: %s", tap, addErr, output)
		}
	}
	if output, err := exec.Command("ip", "link", "set", tap, "master", bridge).CombinedOutput(); err != nil {
		return fmt.Errorf("attach CHV TAP %s to %s: %w: %s", tap, bridge, err, output)
	}
	if output, err := exec.Command("ip", "link", "set", tap, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("bring up CHV TAP %s: %w: %s", tap, err, output)
	}
	return nil
}

func runtimeTap(record sandboxRecord) string {
	if !record.EgressAllowed {
		return ""
	}
	return record.Tap
}
func runtimeMAC(record sandboxRecord) string {
	if !record.EgressAllowed {
		return ""
	}
	return record.MAC
}
func runtimeGuestIP(record sandboxRecord) string {
	if !record.EgressAllowed {
		return ""
	}
	return record.GuestIP
}
func runtimeGateway(record sandboxRecord) string {
	if !record.EgressAllowed {
		return ""
	}
	return record.Gateway
}
