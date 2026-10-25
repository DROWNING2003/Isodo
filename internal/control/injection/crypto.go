package injection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Sealer seals rule material with AES-256-GCM. The key never enters the
// database: it comes from ISODO_INJECTION_KEY (base64, 32 bytes) or a 0600 key
// file beside the development certificates.
type Sealer struct {
	aead cipher.AEAD
}

func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("injection key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create injection cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create injection AEAD: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plaintext []byte) (nonce, sealed []byte, err error) {
	nonce = make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate injection nonce: %w", err)
	}
	return nonce, s.aead.Seal(nil, nonce, plaintext, nil), nil
}

func (s *Sealer) Open(nonce, sealed []byte) ([]byte, error) {
	if len(nonce) != s.aead.NonceSize() {
		return nil, errors.New("injection nonce has an invalid length")
	}
	plaintext, err := s.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, errors.New("injection material could not be decrypted")
	}
	return plaintext, nil
}

// LoadKey resolves the sealing key. Production deployments must set
// ISODO_INJECTION_KEY. Local development may use a generated 0600 key file so
// restarting the API keeps existing rules readable.
func LoadKey(explicit, keyFile string) ([]byte, error) {
	if strings.TrimSpace(explicit) != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(explicit))
		if err != nil {
			key, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(explicit))
		}
		if err != nil || len(key) != 32 {
			return nil, errors.New("ISODO_INJECTION_KEY must be base64 for exactly 32 bytes")
		}
		return key, nil
	}
	if strings.TrimSpace(keyFile) == "" {
		return nil, errors.New("ISODO_INJECTION_KEY or ISODO_INJECTION_KEY_FILE is required")
	}
	content, err := os.ReadFile(keyFile)
	if err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(content)))
		if decodeErr != nil || len(key) != 32 {
			return nil, fmt.Errorf("read injection key file %q: key must be base64 for 32 bytes", keyFile)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read injection key file: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate injection key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, fmt.Errorf("create injection key directory: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyFile, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write injection key file: %w", err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return nil, fmt.Errorf("restrict injection key file: %w", err)
	}
	return key, nil
}
