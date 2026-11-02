// Package mcpclient implements the bounded trusted-side Streamable HTTP client.
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const Endpoint = "https://api.githubcopilot.com/mcp/readonly"
const Protocol = "2025-06-18"
const MaxResponse = 8 << 20

// The allowlist is deliberately smaller than GitHub's readonly toolset. Do not
// trust annotations or a read-looking name supplied by an upstream tool.
func Allowed(name string) bool {
	switch name {
	case "get_me", "get_file_contents", "search_repositories", "search_code", "list_branches", "list_commits", "get_commit", "list_issues", "issue_read", "search_issues", "list_pull_requests", "pull_request_read", "search_pull_requests":
		return true
	}
	return false
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type Session struct {
	Client                       *http.Client
	Endpoint, Token, ID, Version string
	next                         int
}
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("MCP upstream HTTP %d", e.Status) }

var ErrProtocol = errors.New("MCP upstream protocol error")

func (s *Session) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.next++
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	notify := strings.HasPrefix(method, "notifications/")
	if !notify {
		message["id"] = s.next
	}
	data, err := json.Marshal(message)
	if err != nil {
		return nil, ErrProtocol
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.Endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, ErrProtocol
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if s.ID != "" {
		req.Header.Set("Mcp-Session-Id", s.ID)
	}
	if s.Version != "" {
		req.Header.Set("MCP-Protocol-Version", s.Version)
	}
	client := *s.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("MCP upstream unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{resp.StatusCode}
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		if len(id) > 256 || strings.ContainsAny(id, "\r\n") {
			return nil, ErrProtocol
		}
		s.ID = id
	}
	if notify {
		return nil, nil
	}
	var raw []byte
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, MaxResponse+1))
		scanner.Buffer(make([]byte, 4096), MaxResponse)
		var event strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if event.Len() > 0 {
					var envelope struct {
						ID json.RawMessage `json:"id"`
					}
					if json.Unmarshal([]byte(event.String()), &envelope) == nil && string(envelope.ID) == fmt.Sprint(s.next) {
						raw = []byte(event.String())
						break
					}
					event.Reset()
				}
				continue
			}
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				event.WriteString(strings.TrimPrefix(rest, " "))
				event.WriteByte('\n')
			}
		}
		if len(raw) == 0 {
			return nil, ErrProtocol
		}
	} else {
		raw, err = io.ReadAll(io.LimitReader(resp.Body, MaxResponse+1))
		if err != nil || len(raw) > MaxResponse {
			return nil, ErrProtocol
		}
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.JSONRPC != "2.0" || string(envelope.ID) != fmt.Sprint(s.next) || len(envelope.Result) == 0 || len(envelope.Error) > 0 {
		return nil, ErrProtocol
	}
	return envelope.Result, nil
}
func (s *Session) Initialize(ctx context.Context) error {
	result, err := s.Request(ctx, "initialize", map[string]any{"protocolVersion": Protocol, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "isodo", "version": "1"}})
	if err != nil {
		return err
	}
	var init struct {
		Version string `json:"protocolVersion"`
	}
	if json.Unmarshal(result, &init) != nil {
		return ErrProtocol
	}
	switch init.Version {
	case "2025-03-26", Protocol, "2025-11-25":
		s.Version = init.Version
	default:
		return ErrProtocol
	}
	_, err = s.Request(ctx, "notifications/initialized", map[string]any{})
	return err
}
func (s *Session) Tools(ctx context.Context) ([]Tool, error) {
	tools := []Tool{}
	cursor := ""
	seen := map[string]bool{}
	for range 20 {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := s.Request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools []Tool `json:"tools"`
			Next  string `json:"nextCursor"`
		}
		if json.Unmarshal(result, &page) != nil {
			return nil, ErrProtocol
		}
		for _, tool := range page.Tools {
			if Allowed(tool.Name) {
				tools = append(tools, tool)
			}
		}
		if len(tools) > 512 {
			return nil, ErrProtocol
		}
		if page.Next == "" {
			return tools, nil
		}
		if seen[page.Next] {
			return nil, ErrProtocol
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	return nil, ErrProtocol
}
func (s *Session) Close(ctx context.Context) {
	if s.ID == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", s.Endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Mcp-Session-Id", s.ID)
	req.Header.Set("MCP-Protocol-Version", s.Version)
	client := *s.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
