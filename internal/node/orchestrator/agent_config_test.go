package orchestrator

import "testing"

func TestAgentConfigPathMatchesRuntimeHome(t *testing.T) {
	for input, want := range map[string]string{
		"~/.codex/config.toml":    "/workspace/.codex/config.toml",
		"~/.codex/auth.json":      "/workspace/.codex/auth.json",
		"~/.claude/settings.json": "/workspace/.home/.claude/settings.json",
	} {
		got, err := agentGuestPath(input)
		if err != nil || got != want {
			t.Errorf("agentGuestPath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}
