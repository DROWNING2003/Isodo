package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrUnavailable     = errors.New("identity service unavailable")
)

type Identity struct {
	Issuer      string
	Subject     string
	DisplayName string
	TenantID    string
	Credential  string
	Scopes      []string
}

// ProviderEndpointPolicy can opt one private-IP HTTP authority into test-only
// OIDC deployments. HTTPS and loopback HTTP remain the defaults.
type ProviderEndpointPolicy struct {
	// AllowInsecureHTTPHost is an exact private-IP host:port accepted over HTTP.
	AllowInsecureHTTPHost string
}

func (p ProviderEndpointPolicy) validate() error {
	if p.AllowInsecureHTTPHost == "" {
		return nil
	}

	endpoint, err := url.Parse("http://" + p.AllowInsecureHTTPHost)
	if err != nil || endpoint.Host != p.AllowInsecureHTTPHost || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() == "" {
		return errors.New("ISODO_CASDOOR_INSECURE_HTTP_HOST must be an exact private IP:port")
	}
	address, err := netip.ParseAddr(endpoint.Hostname())
	if err != nil || !address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() || address.IsMulticast() {
		return errors.New("ISODO_CASDOOR_INSECURE_HTTP_HOST must be a private unicast IP:port")
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("ISODO_CASDOOR_INSECURE_HTTP_HOST must contain a valid port")
	}
	return nil
}

func (p ProviderEndpointPolicy) allowsInsecureHTTP(endpoint *url.URL) bool {
	return p.AllowInsecureHTTPHost != "" && strings.EqualFold(endpoint.Host, p.AllowInsecureHTTPHost) && p.validate() == nil
}

type IdentityStore interface {
	ResolveTenant(ctx context.Context, issuer, subject, displayName string) (string, error)
}

type discoveryDocument struct {
	Issuer      string `json:"issuer"`
	UserInfoURL string `json:"userinfo_endpoint"`
}

type claims struct {
	Issuer        string   `json:"iss"`
	Subject       string   `json:"sub"`
	Audience      audience `json:"aud"`
	Name          string   `json:"name"`
	PreferredUser string   `json:"preferred_username"`
}

type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("decode audience: %w", err)
	}
	*a = many
	return nil
}

type Casdoor struct {
	issuer        string
	audience      string
	userInfoURL   string
	client        *http.Client
	identityStore IdentityStore
}

func NewCasdoor(ctx context.Context, issuer, clientID string, identityStore IdentityStore, client *http.Client) (*Casdoor, error) {
	return NewCasdoorWithPolicy(ctx, issuer, clientID, identityStore, client, ProviderEndpointPolicy{})
}

// NewCasdoorWithPolicy configures Casdoor using the supplied endpoint policy.
func NewCasdoorWithPolicy(ctx context.Context, issuer, clientID string, identityStore IdentityStore, client *http.Client, policy ProviderEndpointPolicy) (*Casdoor, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Host == "" || issuerURL.User != nil || issuerURL.RawQuery != "" || issuerURL.Fragment != "" || !validProviderEndpoint(issuer, policy) {
		return nil, errors.New("Casdoor issuer must use HTTPS, loopback HTTP, or the explicitly allowlisted private HTTP authority")
	}
	if clientID == "" || identityStore == nil {
		return nil, errors.New("Casdoor client ID and identity store are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	issuer = strings.TrimRight(issuer, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, fmt.Errorf("create OIDC discovery request: %w", err)
	}
	response, err := clientCopy.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch OIDC discovery: %w", ErrUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery returned %s: %w", response.Status, ErrUnavailable)
	}
	discoveryBody, err := io.ReadAll(io.LimitReader(response.Body, (32<<10)+1))
	if err != nil || len(discoveryBody) > 32<<10 {
		return nil, fmt.Errorf("read OIDC discovery: %w", ErrUnavailable)
	}
	var document discoveryDocument
	if err := json.Unmarshal(discoveryBody, &document); err != nil {
		return nil, fmt.Errorf("decode OIDC discovery: %w", ErrUnavailable)
	}
	if document.Issuer != issuer || !validProviderEndpoint(document.UserInfoURL, policy) {
		return nil, errors.New("OIDC discovery issuer mismatch or invalid userinfo endpoint")
	}
	return &Casdoor{
		issuer:        issuer,
		audience:      clientID,
		userInfoURL:   document.UserInfoURL,
		client:        &clientCopy,
		identityStore: identityStore,
	}, nil
}

func (c *Casdoor) Authenticate(ctx context.Context, authorization string) (Identity, error) {
	if c == nil {
		return Identity{}, ErrUnavailable
	}
	token, ok := parseBearer(authorization)
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userInfoURL, nil)
	if err != nil {
		return Identity{}, ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.client.Do(request)
	if err != nil {
		return Identity{}, fmt.Errorf("Casdoor UserInfo request: %w", ErrUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Identity{}, ErrUnauthenticated
	}
	if response.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("Casdoor UserInfo returned %s: %w", response.Status, ErrUnavailable)
	}
	userInfoBody, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(userInfoBody) > 16<<10 {
		return Identity{}, fmt.Errorf("read Casdoor UserInfo: %w", ErrUnavailable)
	}
	var user claims
	if err := json.Unmarshal(userInfoBody, &user); err != nil {
		return Identity{}, fmt.Errorf("decode Casdoor UserInfo: %w", ErrUnavailable)
	}
	if user.Subject == "" || len(user.Subject) > 1024 || user.Issuer != c.issuer || !contains(user.Audience, c.audience) {
		return Identity{}, ErrUnauthenticated
	}
	displayName := user.PreferredUser
	if displayName == "" {
		displayName = user.Name
	}
	tenantID, err := c.identityStore.ResolveTenant(ctx, c.issuer, user.Subject, displayName)
	if err != nil {
		return Identity{}, fmt.Errorf("resolve local identity: %w", ErrUnavailable)
	}
	return Identity{Issuer: c.issuer, Subject: user.Subject, DisplayName: displayName, TenantID: tenantID}, nil
}

func parseBearer(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func validProviderEndpoint(raw string, policy ProviderEndpointPolicy) bool {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return false
	}
	if endpoint.Scheme == "https" || (endpoint.Scheme == "http" && isLoopbackHost(endpoint.Hostname())) {
		return true
	}
	return endpoint.Scheme == "http" && policy.allowsInsecureHTTP(endpoint)
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}
