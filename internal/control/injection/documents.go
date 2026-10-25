package injection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
)

type CredentialDocument struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}
type SealedDocument struct {
	ID                string
	Nonce, Ciphertext []byte
}
type documentStore interface {
	ListDocuments(context.Context, string, string) ([]SealedDocument, error)
	PutDocument(context.Context, string, string, SealedDocument) error
	DeleteDocument(context.Context, string, string, string) error
}

func (s *PgStore) ListDocuments(ctx context.Context, tenant, kind string) ([]SealedDocument, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,nonce,ciphertext FROM credential_documents WHERE tenant_id=$1::uuid AND kind=$2 ORDER BY updated_at DESC,id`, tenant, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SealedDocument{}
	for rows.Next() {
		var item SealedDocument
		if err := rows.Scan(&item.ID, &item.Nonce, &item.Ciphertext); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *PgStore) PutDocument(ctx context.Context, tenant, kind string, item SealedDocument) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO credential_documents (tenant_id,kind,id,nonce,ciphertext) VALUES($1::uuid,$2,$3,$4,$5) ON CONFLICT(tenant_id,kind,id) DO UPDATE SET nonce=excluded.nonce,ciphertext=excluded.ciphertext,updated_at=now()`, tenant, kind, item.ID, item.Nonce, item.Ciphertext)
	return err
}
func (s *PgStore) DeleteDocument(ctx context.Context, tenant, kind, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM credential_documents WHERE tenant_id=$1::uuid AND kind=$2 AND id=$3`, tenant, kind, id)
	return err
}
func (h *Handler) CredentialDocuments(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if !slices.Contains([]string{"agents", "sandboxes"}, kind) || len(id) > 160 {
		fail(w, 400, "INVALID_REQUEST", "Invalid document identifier.")
		return
	}
	store, ok := h.store.(documentStore)
	if !ok {
		fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Credential configuration storage is unavailable.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := store.ListDocuments(r.Context(), identity.TenantID, kind)
		if err != nil {
			fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Could not load credential configurations.")
			return
		}
		result := []CredentialDocument{}
		for _, item := range items {
			value, err := h.sealer.Open(item.Nonce, item.Ciphertext)
			if err != nil {
				fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Could not read credential configuration.")
				return
			}
			result = append(result, CredentialDocument{item.ID, value})
		}
		write(w, 200, map[string]any{"items": result})
	case http.MethodPut:
		if strings.TrimSpace(id) == "" {
			fail(w, 400, "INVALID_REQUEST", "Missing document identifier.")
			return
		}
		var value json.RawMessage
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
		if err := dec.Decode(&value); err != nil {
			fail(w, 400, "INVALID_REQUEST", "Invalid configuration.")
			return
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			fail(w, 400, "INVALID_REQUEST", "Invalid configuration.")
			return
		}
		if kind == "agents" {
			var config struct {
				ID           string   `json:"id"`
				Name         string   `json:"name"`
				Agent        string   `json:"agent"`
				ConnectionID string   `json:"connectionId"`
				FilePath     string   `json:"filePath"`
				DefaultModel string   `json:"defaultModel"`
				ModelNames   []string `json:"modelNames"`
				Content      string   `json:"content"`
			}
			if json.Unmarshal(value, &config) != nil {
				fail(w, 400, "INVALID_REQUEST", "Invalid Agent configuration.")
				return
			}
			if config.ID != id || strings.TrimSpace(config.Name) == "" || len(config.Name) > 128 || config.FilePath == "" || config.DefaultModel == "" || config.ModelNames == nil || len(config.ModelNames) > 1000 || !slices.Contains([]string{"codex", "claude", "opencode", "cursor", "grok"}, config.Agent) {
				fail(w, 400, "INVALID_REQUEST", "Incomplete Agent configuration.")
				return
			}
			if _, err := h.store.Get(r.Context(), identity.TenantID, config.ConnectionID); err != nil {
				fail(w, 400, "INVALID_REQUEST", "Referenced credential is unavailable.")
				return
			}
		} else {
			var config struct {
				AgentIDs      []string          `json:"agentIds"`
				CredentialIDs []string          `json:"credentialIds"`
				Models        []json.RawMessage `json:"models"`
			}
			if json.Unmarshal(value, &config) != nil || config.AgentIDs == nil || config.CredentialIDs == nil || config.Models == nil {
				fail(w, 400, "INVALID_REQUEST", "Invalid sandbox credential selection.")
				return
			}
		}
		nonce, sealed, err := h.sealer.Seal(value)
		if err != nil {
			fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Could not protect configuration.")
			return
		}
		if err := store.PutDocument(r.Context(), identity.TenantID, kind, SealedDocument{id, nonce, sealed}); err != nil {
			fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Could not save configuration.")
			return
		}
		write(w, 200, CredentialDocument{id, value})
	case http.MethodDelete:
		if id == "" {
			fail(w, 400, "INVALID_REQUEST", "Missing document identifier.")
			return
		}
		if err := store.DeleteDocument(r.Context(), identity.TenantID, kind, id); err != nil {
			fail(w, 503, "DOCUMENTS_UNAVAILABLE", "Could not delete configuration.")
			return
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func (h *Handler) ListCredentialDocuments(w http.ResponseWriter, r *http.Request, kind string) {
	h.CredentialDocuments(w, r)
}
func (h *Handler) PutCredentialDocument(w http.ResponseWriter, r *http.Request, kind string, id string) {
	h.CredentialDocuments(w, r)
}
func (h *Handler) DeleteCredentialDocument(w http.ResponseWriter, r *http.Request, kind string, id string) {
	h.CredentialDocuments(w, r)
}
