package injection

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreEncryptsAndPreservesSecrets(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager(ManagerConfig{StateDir: dir, HostGateway: "192.168.5.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	rule := Rule{
		Key: "one-off:one", Name: "temporary", Kind: KindOneOff,
		Spec:   Spec{Type: TypeOpenAI, BaseURL: "https://api.openai.com"},
		Secret: Secret{APIKey: "sk-secret"},
	}
	config, err := manager.SetRules("sandbox-a", []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || len(config.Rules) != 1 || config.Rules[0].Secret.APIKey != "" {
		t.Fatalf("masked config = %+v", config)
	}
	if config.Rules[0].SecretHint == "" {
		t.Fatal("missing secret hint")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "injections.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || string(raw) == "" {
		t.Fatal("empty encrypted store")
	}
	for _, marker := range []string{"sk-secret", "temporary", "api.openai.com"} {
		if containsBytes(raw, marker) {
			t.Fatalf("encrypted store leaked %q", marker)
		}
	}
	// An update that omits the secret keeps the existing material.
	rule.Secret = Secret{}
	updated, err := manager.SetRules("sandbox-a", []Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Rules[0].SecretHint == "" {
		t.Fatal("secret hint lost on update")
	}
	full := manager.store.Get("sandbox-a")
	if full.Rules[0].Secret.APIKey != "sk-secret" {
		t.Fatalf("secret not preserved: %+v", full.Rules[0].Secret)
	}
	// Reloading from disk must decrypt with the key file.
	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("sandbox-a"); len(got.Rules) != 1 || got.Rules[0].Secret.APIKey != "sk-secret" {
		t.Fatalf("reloaded = %+v", got)
	}
	if err := manager.Delete("sandbox-a"); err != nil {
		t.Fatal(err)
	}
	if got := manager.Get("sandbox-a"); len(got.Rules) != 0 {
		t.Fatal("delete left rules behind")
	}
}

func containsBytes(haystack []byte, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && string(haystack) != "" && indexOf(haystack, []byte(needle)) >= 0
}

func indexOf(haystack, needle []byte) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		match := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				match = false
				break
			}
		}
		if match {
			return index
		}
	}
	return -1
}

func TestRemovePersistentRevokesEverySandbox(t *testing.T) {
	manager, err := NewManager(ManagerConfig{StateDir: t.TempDir(), HostGateway: "192.168.5.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	persistent := Rule{
		Key: "persistent:rule-1", Name: "account", Kind: KindPersistent, PersistentID: "rule-1",
		Spec:   Spec{Type: TypeGitHub, BaseURL: "https://api.github.com"},
		Secret: Secret{Token: "ghp_secret"},
	}
	oneOff := Rule{
		Key: "one-off:rule-2", Name: "temp", Kind: KindOneOff,
		Spec: Spec{Type: TypeOpenAI}, Secret: Secret{APIKey: "sk-secret"},
	}
	if _, err := manager.SetRules("sandbox-a", []Rule{persistent, oneOff}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetRules("sandbox-b", []Rule{persistent}); err != nil {
		t.Fatal(err)
	}
	if got := manager.Summary()["rule-1"]; got != 2 {
		t.Fatalf("summary = %d", got)
	}
	if err := manager.RemovePersistentRule("rule-1"); err != nil {
		t.Fatal(err)
	}
	a := manager.store.Get("sandbox-a")
	if len(a.Rules) != 1 || a.Rules[0].Kind != KindOneOff {
		t.Fatalf("sandbox-a should keep only the one-off rule: %+v", a.Rules)
	}
	b := manager.store.Get("sandbox-b")
	if len(b.Rules) != 0 {
		t.Fatalf("sandbox-b still has rules: %+v", b.Rules)
	}
	if proxy := manager.proxies["sandbox-a"]; proxy != nil {
		if rules := proxy.Rules(); len(rules) != 1 || rules[0].Kind != KindOneOff {
			t.Fatalf("sandbox-a proxy rules = %+v", rules)
		}
	}
	if proxy := manager.proxies["sandbox-b"]; proxy != nil && len(proxy.Rules()) != 0 {
		t.Fatalf("sandbox-b proxy still has rules")
	}
	if got := manager.Summary()["rule-1"]; got != 0 {
		t.Fatalf("summary after revoke = %d", got)
	}
}
