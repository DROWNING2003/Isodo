package injection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store keeps sandbox rule sets at rest. The whole document is AES-256-GCM
// sealed because one-off rules and references both contain material; the key
// is a 0600 file beside it, generated on first use.
type Store struct {
	mu   sync.Mutex
	path string
	key  []byte
	data map[string]SandboxConfig
}

func NewStore(stateDir string) (*Store, error) {
	if stateDir == "" {
		return nil, errors.New("injection state directory is required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create injection state directory: %w", err)
	}
	key, err := loadOrCreateKey(filepath.Join(stateDir, "injections.key"))
	if err != nil {
		return nil, err
	}
	store := &Store{path: filepath.Join(stateDir, "injections.enc"), key: key, data: map[string]SandboxConfig{}}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(string(content))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("injection key file is invalid")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read injection key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate injection key: %w", err)
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("write injection key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict injection key: %w", err)
	}
	return key, nil
}

func (s *Store) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) load() error {
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read injection store: %w", err)
	}
	aead, err := s.aead()
	if err != nil {
		return err
	}
	if len(content) < aead.NonceSize() {
		return errors.New("injection store is truncated")
	}
	nonce, sealed := content[:aead.NonceSize()], content[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return errors.New("injection store could not be decrypted")
	}
	if len(plaintext) == 0 {
		return nil
	}
	if err := json.Unmarshal(plaintext, &s.data); err != nil {
		return err
	}
	if s.data == nil {
		s.data = map[string]SandboxConfig{}
	}
	return nil
}

func (s *Store) saveLocked() error {
	aead, err := s.aead()
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, plaintext, nil)
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, append(nonce, sealed...), 0o600); err != nil {
		return fmt.Errorf("write injection store: %w", err)
	}
	return os.Rename(temporary, s.path)
}

func (s *Store) Get(sandboxID string) SandboxConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	config := s.data[sandboxID]
	return cloneConfig(config)
}

func (s *Store) Set(sandboxID string, config SandboxConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	config.UpdatedAt = time.Now().UTC()
	config.Enabled = len(config.Rules) > 0
	s.data[sandboxID] = cloneConfig(config)
	return s.saveLocked()
}

func (s *Store) Delete(sandboxID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, sandboxID)
	return s.saveLocked()
}

// Summary counts persistent-rule attachments across known sandboxes.
func (s *Store) Summary() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]int{}
	for _, config := range s.data {
		for _, rule := range config.Rules {
			if rule.Kind == KindPersistent && rule.PersistentID != "" {
				result[rule.PersistentID]++
			}
		}
	}
	return result
}

func cloneConfig(config SandboxConfig) SandboxConfig {
	clone := config
	clone.Rules = make([]Rule, 0, len(config.Rules))
	for _, rule := range config.Rules {
		copied := rule
		copied.Spec.IfHeaders = cloneStringMap(rule.Spec.IfHeaders)
		copied.Spec.IfQueries = cloneStringMap(rule.Spec.IfQueries)
		if rule.Secret.Headers != nil {
			copied.Secret.Headers = cloneStringMap(rule.Secret.Headers)
		}
		clone.Rules = append(clone.Rules, copied)
	}
	return clone
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

// RemovePersistent drops references to one account rule from every sandbox and
// returns the sandboxes whose rule sets changed. It keeps the on-disk document
// encrypted and atomic.
func (s *Store) RemovePersistent(persistentID string) (map[string]SandboxConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := map[string]SandboxConfig{}
	for sandboxID, config := range s.data {
		filtered := make([]Rule, 0, len(config.Rules))
		removed := false
		for _, rule := range config.Rules {
			if rule.Kind == KindPersistent && rule.PersistentID == persistentID {
				removed = true
				continue
			}
			filtered = append(filtered, rule)
		}
		if !removed {
			continue
		}
		config.Rules = filtered
		config.Enabled = len(filtered) > 0
		config.UpdatedAt = time.Now().UTC()
		s.data[sandboxID] = cloneConfig(config)
		changed[sandboxID] = cloneConfig(config)
	}
	if len(changed) == 0 {
		return changed, nil
	}
	return changed, s.saveLocked()
}
