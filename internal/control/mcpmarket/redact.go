package mcpmarket

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Redact decoded JSON strings, including keys. Byte replacement alone misses
// secrets escaped by a JSON encoder (quotes, backslashes or Unicode escapes).
func redactJSON(raw []byte, secrets ...string) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	clean := func(s string) string {
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "[REDACTED]")
			}
		}
		return s
	}
	var visit func(any) any
	visit = func(v any) any {
		switch x := v.(type) {
		case string:
			return clean(x)
		case []any:
			for i, item := range x {
				x[i] = visit(item)
			}
		case map[string]any:
			result := make(map[string]any, len(x))
			for k, item := range x {
				result[clean(k)] = visit(item)
			}
			return result
		}
		return v
	}
	return json.Marshal(visit(value))
}
