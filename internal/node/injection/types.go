// Package injection applies account-level and sandbox-local secure-connection
// rules to outbound HTTPS traffic from a local sandbox. It is deliberately
// separate from internal/control/injection: the control plane stores account
// rules, while this runtime holds the material needed to inject on the host.
package injection

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	KindPersistent = "persistent"
	KindOneOff     = "one-off"

	TypeHTTP      = "http"
	TypeOpenAI    = "openai"
	TypeAnthropic = "anthropic"
	TypeGemini    = "gemini"
	TypeQiniu     = "qiniu"
	TypeGitHub    = "github"
)

var validTypes = map[string]bool{
	TypeHTTP: true, TypeOpenAI: true, TypeAnthropic: true,
	TypeGemini: true, TypeQiniu: true, TypeGitHub: true,
}

// Secret is the plaintext material for one rule. Exactly one field is used
// according to Spec.Type. It is never returned to the web client.
type Secret struct {
	APIKey  string            `json:"api_key,omitempty"`
	Token   string            `json:"token,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Spec is the non-secret match and injection shape.
type Spec struct {
	Type       string            `json:"type"`
	BaseURL    string            `json:"base_url,omitempty"`
	HeaderName string            `json:"header_name,omitempty"`
	IfHeaders  map[string]string `json:"if_headers,omitempty"`
	IfQueries  map[string]string `json:"if_queries,omitempty"`
}

// Rule is one effective rule attached to a sandbox.
type Rule struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	PersistentID string    `json:"persistent_id,omitempty"`
	Spec         Spec      `json:"spec"`
	Secret       Secret    `json:"secret,omitempty"`
	SecretHint   string    `json:"secret_hint,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// SandboxConfig is the persisted rule set for one sandbox.
type SandboxConfig struct {
	Rules     []Rule    `json:"rules"`
	UpdatedAt time.Time `json:"updated_at"`
	Enabled   bool      `json:"enabled"`
}

// View strips plaintext material for API/UI responses.
func (r Rule) View() Rule {
	view := r
	view.Secret = Secret{}
	return view
}

// ConfigView strips all plaintext material.
func (c SandboxConfig) View() SandboxConfig {
	view := c
	view.Rules = make([]Rule, 0, len(c.Rules))
	for _, rule := range c.Rules {
		view.Rules = append(view.Rules, rule.View())
	}
	return view
}

// Normalize validates one rule and returns it without touching the secret.
func Normalize(rule Rule) (Rule, error) {
	rule.Key = strings.TrimSpace(rule.Key)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Kind = strings.TrimSpace(rule.Kind)
	if rule.Kind == "" {
		rule.Kind = KindOneOff
	}
	if rule.Kind != KindPersistent && rule.Kind != KindOneOff {
		return Rule{}, errors.New("rule kind must be persistent or one-off")
	}
	if rule.Name == "" || len(rule.Name) > 64 {
		return Rule{}, errors.New("rule name must contain 1 to 64 bytes")
	}
	if rule.Key == "" || len(rule.Key) > 128 {
		return Rule{}, errors.New("rule key is required and must be at most 128 bytes")
	}
	rule.Spec.Type = strings.ToLower(strings.TrimSpace(rule.Spec.Type))
	if !validTypes[rule.Spec.Type] {
		return Rule{}, fmt.Errorf("unsupported injection type %q", rule.Spec.Type)
	}
	rule.Spec.BaseURL = strings.TrimRight(strings.TrimSpace(rule.Spec.BaseURL), "/")
	if rule.Spec.BaseURL != "" {
		normalized, err := normalizeBaseURL(rule.Spec.BaseURL)
		if err != nil {
			return Rule{}, err
		}
		rule.Spec.BaseURL = normalized
	}
	if rule.Spec.Type == TypeHTTP && rule.Spec.BaseURL == "" {
		return Rule{}, errors.New("HTTP injection requires a base URL")
	}
	if rule.Spec.Type == TypeHTTP && strings.TrimSpace(rule.Spec.HeaderName) == "" {
		rule.Spec.HeaderName = "Authorization"
	}
	if rule.Spec.HeaderName != "" && !validHeaderName(rule.Spec.HeaderName) {
		return Rule{}, errors.New("header name contains unsupported characters")
	}
	if rule.Spec.Type != TypeHTTP && rule.Spec.HeaderName != "" {
		return Rule{}, errors.New("header name is only supported for HTTP injection")
	}
	if len(rule.Spec.IfHeaders) > 20 || len(rule.Spec.IfQueries) > 20 {
		return Rule{}, errors.New("at most 20 header and query conditions are supported")
	}
	if err := validateConditions(rule.Spec.IfHeaders); err != nil {
		return Rule{}, err
	}
	if err := validateConditions(rule.Spec.IfQueries); err != nil {
		return Rule{}, err
	}
	if rule.Spec.Type == TypeGitHub && rule.Spec.BaseURL != "" {
		parsed, err := url.Parse(rule.Spec.BaseURL)
		if err != nil || (parsed.Hostname() != "github.com" && parsed.Hostname() != "api.github.com") {
			return Rule{}, errors.New("GitHub injection must match github.com or api.github.com")
		}
	}
	if secretValue(rule) == "" {
		return Rule{}, errors.New("secret is required")
	}
	return rule, nil
}

func secretValue(rule Rule) string {
	switch rule.Spec.Type {
	case TypeHTTP:
		for _, value := range rule.Secret.Headers {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	case TypeGitHub:
		return strings.TrimSpace(rule.Secret.Token)
	default:
		return strings.TrimSpace(rule.Secret.APIKey)
	}
}

func normalizeBaseURL(value string) (string, error) {
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return "", errors.New("base URL must be an HTTPS URL without credentials, query or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback(parsed.Hostname())) {
		return "", errors.New("base URL must use HTTPS (HTTP is allowed only on loopback)")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for index, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", char):
		default:
			return false
		}
		if index == 0 && strings.ContainsRune("!#$%&'*+-.^_`|~0123456789", char) {
			return false
		}
	}
	return true
}

func validateConditions(values map[string]string) error {
	for key, value := range values {
		if strings.TrimSpace(key) == "" || len(key) > 128 || len(value) > 1024 {
			return errors.New("condition names and values must be non-empty and bounded")
		}
	}
	return nil
}

// Hint summarizes a secret without keeping enough to reconstruct it.
func Hint(rule Rule) string {
	value := secretValue(rule)
	if len(value) <= 4 {
		return "••••"
	}
	return "••••" + value[len(value)-4:]
}

// WithHint returns a view-safe copy with the hint filled from plaintext.
func (r Rule) WithHint() Rule {
	view := r.View()
	view.SecretHint = Hint(r)
	view.UpdatedAt = time.Now().UTC()
	return view
}
