package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/localapi"
)

// WriteAgentConfig writes redacted Agent configuration into the sandbox home.
// The caller supplies no secret material; authentication remains the injection
// manager's responsibility.
func (s *LimaServer) WriteAgentConfig(ctx context.Context, sandboxID, path string, content []byte) (localapi.AgentConfigWriteResult, error) {
	record, ok := s.journal.getSandbox(sandboxID)
	if !ok || record.State != "running" || record.BootID == "" || record.GuestEpoch == "" {
		return localapi.AgentConfigWriteResult{}, fmt.Errorf("sandbox %q is not running", sandboxID)
	}
	guestPath, err := agentGuestPath(path)
	if err != nil {
		return localapi.AgentConfigWriteResult{}, err
	}
	guest, identity, err := s.guestClient(record)
	if err != nil {
		return localapi.AgentConfigWriteResult{}, err
	}
	defer guest.close()
	parent := filepath.Dir(guestPath)
	if _, err := guest.client.MakeDirectory(ctx, &guestv1.MakeDirectoryRequest{Identity: identity, RequestId: "agent-config-dir-" + newT3RequestSuffix(), Path: parent, Parents: true}); err != nil {
		return localapi.AgentConfigWriteResult{}, fmt.Errorf("create Agent configuration directory: %w", err)
	}
	if err := writeGuestFile(ctx, guest.client, identity, "agent-config-"+newT3RequestSuffix(), guestPath, content); err != nil {
		return localapi.AgentConfigWriteResult{}, fmt.Errorf("write Agent configuration: %w", err)
	}
	digest := sha256.Sum256(content)
	return localapi.AgentConfigWriteResult{Path: path, Sha256: hex.EncodeToString(digest[:]), Bytes: len(content), RequiresRestart: true}, nil
}

func agentGuestPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("Agent configuration path is invalid")
	}
	if strings.HasPrefix(path, "~/") {
		path = strings.TrimPrefix(path, "~/")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("Agent configuration path escapes the sandbox home")
	}
	if strings.HasPrefix(clean, ".codex/") {
		return filepath.Join("/workspace", clean), nil
	}
	return filepath.Join("/workspace/.home", clean), nil
}
