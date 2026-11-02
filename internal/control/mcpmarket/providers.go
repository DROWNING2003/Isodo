package mcpmarket

import (
	"net/url"
	"slices"

	"github.com/DROWNING2003/Isodo/internal/mcpclient"
)

func providerID(ids []string) string {
	if len(ids) > 0 {
		return ids[0]
	}
	return "github"
}
func officialProvider(id string) bool {
	return slices.Contains([]string{"github", "notion"}, id)
}

func providerEndpoint(id string) (string, error) {
	switch id {
	case "github":
		return mcpclient.Endpoint, nil
	case "notion":
		return "https://mcp.notion.com/mcp", nil
	}
	return "", ErrOAuth
}
func oauthURL(provider, raw string) bool {
	if provider == "github" {
		return trustedURL(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Port() != "" || u.RawQuery != "" || u.ForceQuery {
		return false
	}
	switch provider {
	case "notion":
		return u.Host == "mcp.notion.com"
	}
	return false
}
func readonlyTool(provider, name string) bool {
	switch provider {
	case "github":
		return mcpclient.Allowed(name)
	case "notion":
		return slices.Contains([]string{"notion-search", "notion-fetch", "notion-get-self", "notion-query-data-sources", "notion-get-comments", "notion-get-users", "notion-get-user", "notion-get-teams"}, name)
	}
	return false
}
