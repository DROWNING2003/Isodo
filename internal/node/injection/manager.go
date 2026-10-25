package injection

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manager owns the host-side CA and one proxy listener per sandbox. A proxy is
// started before T3 so rules can change without restarting the guest runtime.
type Manager struct {
	mu          sync.Mutex
	stateDir    string
	hostGateway string
	ca          *CA
	store       *Store
	proxies     map[string]*Proxy
	logger      *slog.Logger
}

type ManagerConfig struct {
	StateDir    string
	HostGateway string
	Logger      *slog.Logger
}

func NewManager(config ManagerConfig) (*Manager, error) {
	stateDir := config.StateDir
	if stateDir == "" {
		return nil, errors.New("injection state directory is required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create injection state directory: %w", err)
	}
	ca, err := LoadOrCreateCA(filepath.Join(stateDir, "ca"))
	if err != nil {
		return nil, err
	}
	store, err := NewStore(stateDir)
	if err != nil {
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	gateway := config.HostGateway
	if gateway == "" {
		gateway = "192.168.5.2"
	}
	return &Manager{
		stateDir: stateDir, hostGateway: gateway, ca: ca, store: store,
		proxies: map[string]*Proxy{}, logger: logger,
	}, nil
}

func (m *Manager) CACertPEM() []byte { return m.ca.PEM() }

func (m *Manager) Get(sandboxID string) SandboxConfig {
	return m.store.Get(sandboxID).View()
}

func (m *Manager) SetRules(sandboxID string, rules []Rule) (SandboxConfig, error) {
	existing := map[string]Rule{}
	for _, rule := range m.store.Get(sandboxID).Rules {
		existing[rule.Key] = rule
	}
	normalized := make([]Rule, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		if secretValue(rule) == "" {
			if old, ok := existing[rule.Key]; ok {
				if rule.Spec.Type != old.Spec.Type || (rule.Spec.Type == TypeHTTP && rule.Spec.HeaderName != old.Spec.HeaderName) {
					return SandboxConfig{}, errors.New("changing type or header requires a new secret")
				}
				rule.Secret = old.Secret
				rule.SecretHint = old.SecretHint
			}
		}
		value, err := Normalize(rule)
		if err != nil {
			return SandboxConfig{}, err
		}
		if seen[value.Key] {
			return SandboxConfig{}, fmt.Errorf("duplicate rule key %q", value.Key)
		}
		seen[value.Key] = true
		value.SecretHint = Hint(value)
		value.UpdatedAt = time.Now().UTC()
		normalized = append(normalized, value)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Kind != normalized[j].Kind {
			return normalized[i].Kind == KindPersistent
		}
		return normalized[i].Name < normalized[j].Name
	})
	config := SandboxConfig{Rules: normalized, Enabled: len(normalized) > 0}
	if err := m.store.Set(sandboxID, config); err != nil {
		return SandboxConfig{}, err
	}
	m.mu.Lock()
	proxy, err := m.ensureProxyLocked(sandboxID)
	m.mu.Unlock()
	if err != nil {
		return SandboxConfig{}, err
	}
	proxy.SetRules(normalized)
	return config.View(), nil
}

func (m *Manager) Delete(sandboxID string) error {
	m.mu.Lock()
	if proxy := m.proxies[sandboxID]; proxy != nil {
		_ = proxy.Close()
		delete(m.proxies, sandboxID)
	}
	m.mu.Unlock()
	return m.store.Delete(sandboxID)
}

// EnsureProxy starts the sandbox proxy if it is not running and applies the
// persisted rules. It is safe to call before every T3 start.
func (m *Manager) EnsureProxy(sandboxID string) error {
	m.mu.Lock()
	proxy, err := m.ensureProxyLocked(sandboxID)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	proxy.SetRules(m.store.Get(sandboxID).Rules)
	return nil
}

func (m *Manager) ensureProxyLocked(sandboxID string) (*Proxy, error) {
	if proxy := m.proxies[sandboxID]; proxy != nil && !proxy.closed.Load() {
		return proxy, nil
	}
	proxy, err := NewProxy(sandboxID, m.ca, m.logger)
	if err != nil {
		return nil, err
	}
	m.proxies[sandboxID] = proxy
	return proxy, nil
}

// Environment returns proxy and CA variables for a guest process. It always
// supplies a proxy so later rule changes take effect immediately.
func (m *Manager) Environment(sandboxID string) (map[string]string, error) {
	if err := m.EnsureProxy(sandboxID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	proxy := m.proxies[sandboxID]
	m.mu.Unlock()
	if proxy == nil {
		return nil, errors.New("injection proxy is not running")
	}
	proxyURL := proxy.URL(m.hostGateway)
	caPath := "/workspace/.isodo/injection-ca.crt"
	return map[string]string{
		"HTTP_PROXY":          proxyURL,
		"HTTPS_PROXY":         proxyURL,
		"ALL_PROXY":           proxyURL,
		"NO_PROXY":            "localhost,127.0.0.1,::1,.local",
		"NODE_EXTRA_CA_CERTS": caPath,
		"SSL_CERT_FILE":       caPath,
		"CURL_CA_BUNDLE":      caPath,
		"REQUESTS_CA_BUNDLE":  caPath,
		"GIT_SSL_CAINFO":      caPath,
		"NPM_CONFIG_CAFILE":   caPath,
	}, nil
}

func (m *Manager) ProxyPort(sandboxID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proxy := m.proxies[sandboxID]; proxy != nil {
		return proxy.Port()
	}
	return 0
}

func (m *Manager) Stats(sandboxID string) Stats {
	m.mu.Lock()
	proxy := m.proxies[sandboxID]
	m.mu.Unlock()
	if proxy == nil {
		return Stats{}
	}
	return proxy.Stats()
}

// RemovePersistentRule revokes an account rule from every local sandbox and
// updates their running proxies immediately.
func (m *Manager) RemovePersistentRule(persistentID string) error {
	changed, err := m.store.RemovePersistent(persistentID)
	if err != nil {
		return err
	}
	for sandboxID, config := range changed {
		m.mu.Lock()
		proxy := m.proxies[sandboxID]
		m.mu.Unlock()
		if proxy != nil {
			proxy.SetRules(config.Rules)
		}
	}
	return nil
}

func (m *Manager) Summary() map[string]int { return m.store.Summary() }

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, proxy := range m.proxies {
		_ = proxy.Close()
		delete(m.proxies, id)
	}
}
