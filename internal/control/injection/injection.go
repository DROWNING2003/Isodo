// Package injection owns account-level HTTP injection rules. A rule matches
// outbound HTTPS requests and adds or replaces authentication headers. Secret
// material is sealed before it reaches PostgreSQL and is only decrypted for
// the authenticated owner or the local desktop runtime that applies it.
package injection

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
)

var (
	ErrNotFound  = errors.New("injection rule not found")
	ErrExists    = errors.New("injection rule already exists")
	ErrForbidden = errors.New("injection rules require an interactive session")
)

const (
	TypeHTTP      = "http"
	TypeMCP       = "mcp"
	TypeOpenAI    = "openai"
	TypeAnthropic = "anthropic"
	TypeGemini    = "gemini"
	TypeQiniu     = "qiniu"
	TypeGitHub    = "github"
)

var validTypes = map[string]bool{
	TypeMCP: true, TypeHTTP: true, TypeOpenAI: true, TypeAnthropic: true,
	TypeGemini: true, TypeQiniu: true, TypeGitHub: true,
}

// Rule is one persisted injection rule. Secret fields never leave this package
// except through Material for the authenticated owner.
type Rule struct {
	ID         string
	TenantID   string
	Name       string
	Type       string
	BaseURL    string
	HeaderName string
	IfHeaders  map[string]string
	IfQueries  map[string]string
	SecretHint string
	Nonce      []byte
	Sealed     []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Material is the plaintext form of a rule's secret. Exactly one of APIKey,
// Token or Headers is set according to Type.
type Material struct {
	APIKey  string            `json:"api_key,omitempty"`
	Token   string            `json:"token,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// View is the safe, secret-free representation returned by list/get.
func (r Rule) View() publicapi.InjectionRule {
	view := publicapi.InjectionRule{
		Id:         r.ID,
		Name:       r.Name,
		Type:       publicapi.InjectionRuleType(r.Type),
		IfHeaders:  mapPtr(r.IfHeaders),
		IfQueries:  mapPtr(r.IfQueries),
		SecretSet:  true,
		SecretHint: r.SecretHint,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
	if r.BaseURL != "" {
		view.BaseUrl = &r.BaseURL
	}
	if r.HeaderName != "" {
		view.HeaderName = &r.HeaderName
	}
	return view
}

func mapPtr(value map[string]string) *map[string]string {
	if len(value) == 0 {
		return nil
	}
	copied := make(map[string]string, len(value))
	for key, item := range value {
		copied[key] = item
	}
	return &copied
}

// Validate checks a create/update request and returns the normalized rule.
func Validate(name, kind, baseURL, headerName string, ifHeaders, ifQueries map[string]string, secret string) (Rule, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !validTypes[kind] {
		return Rule{}, fmt.Errorf("unsupported injection type %q", kind)
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return Rule{}, errors.New("name must contain 1 to 64 bytes")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		normalized, err := normalizeBaseURL(baseURL)
		if err != nil {
			return Rule{}, err
		}
		baseURL = normalized
	}
	if (kind == TypeHTTP || kind == TypeMCP) && baseURL == "" {
		return Rule{}, errors.New("HTTP injection requires a base URL")
	}
	if (kind == TypeHTTP || kind == TypeMCP) && strings.TrimSpace(headerName) == "" {
		headerName = "Authorization"
	}
	if headerName != "" && !validHeaderName(headerName) {
		return Rule{}, errors.New("header name contains unsupported characters")
	}
	if kind != TypeHTTP && kind != TypeMCP && headerName != "" {
		return Rule{}, errors.New("header name is only supported for HTTP injection")
	}
	if len(ifHeaders) > 20 || len(ifQueries) > 20 {
		return Rule{}, errors.New("at most 20 header and query conditions are supported")
	}
	if err := validateConditions(ifHeaders); err != nil {
		return Rule{}, err
	}
	if err := validateConditions(ifQueries); err != nil {
		return Rule{}, err
	}
	if kind == TypeGitHub && baseURL != "" {
		host, err := url.Parse(baseURL)
		if err != nil || (host.Hostname() != "github.com" && host.Hostname() != "api.github.com") {
			return Rule{}, errors.New("GitHub injection must match github.com or api.github.com")
		}
	}
	if strings.TrimSpace(secret) == "" {
		return Rule{}, errors.New("secret is required")
	}
	return Rule{
		Name: name, Type: kind, BaseURL: baseURL, HeaderName: headerName,
		IfHeaders: cloneMap(ifHeaders), IfQueries: cloneMap(ifQueries),
	}, nil
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

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

// BuildMaterial turns a validated request secret into the wire material shape.
func BuildMaterial(kind, headerName, secret string) Material {
	secret = strings.TrimSpace(secret)
	switch kind {
	case TypeMCP:
		if strings.EqualFold(headerName, "Authorization") && !strings.HasPrefix(strings.ToLower(secret), "bearer ") {
			secret = "Bearer " + secret
		}
		return Material{Headers: map[string]string{headerName: secret}}
	case TypeHTTP:
		return Material{Headers: map[string]string{headerName: secret}}
	case TypeGitHub:
		return Material{Token: secret}
	default:
		return Material{APIKey: secret}
	}
}

// Hint returns a short, non-reversible display suffix. It intentionally keeps
// only four characters so it cannot reconstruct a realistic key.
func Hint(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 4 {
		return "••••"
	}
	return "••••" + secret[len(secret)-4:]
}
