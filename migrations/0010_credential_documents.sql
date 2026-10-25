-- +goose Up
CREATE TABLE credential_documents (
 tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('agents', 'sandboxes')),
 id text NOT NULL CHECK (length(id) BETWEEN 1 AND 160),
 nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
 ciphertext bytea NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (tenant_id, kind, id)
);
-- +goose Down
DROP TABLE credential_documents;
