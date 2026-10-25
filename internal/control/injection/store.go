package injection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists account-level rules. Secrets are already sealed by the time
// they reach Create/Update.
type Store interface {
	List(ctx context.Context, tenantID string) ([]Rule, error)
	Get(ctx context.Context, tenantID, ruleID string) (Rule, error)
	Create(ctx context.Context, rule Rule) (Rule, error)
	Update(ctx context.Context, rule Rule) (Rule, error)
	Delete(ctx context.Context, tenantID, ruleID string) error
}

type PgStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

func (s *PgStore) List(ctx context.Context, tenantID string) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, tenant_id::text, name, type, base_url, header_name,
		       if_headers, if_queries, secret_hint, secret_nonce, secret_ciphertext,
		       created_at, updated_at
		FROM injection_rules WHERE tenant_id = $1::uuid
		ORDER BY created_at DESC, id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list injection rules: %w", err)
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *PgStore) Get(ctx context.Context, tenantID, ruleID string) (Rule, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, name, type, base_url, header_name,
		       if_headers, if_queries, secret_hint, secret_nonce, secret_ciphertext,
		       created_at, updated_at
		FROM injection_rules WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, ruleID)
	rule, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	return rule, err
}

func (s *PgStore) Create(ctx context.Context, rule Rule) (Rule, error) {
	ifHeaders, err := json.Marshal(orEmptyMap(rule.IfHeaders))
	if err != nil {
		return Rule{}, err
	}
	ifQueries, err := json.Marshal(orEmptyMap(rule.IfQueries))
	if err != nil {
		return Rule{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO injection_rules
		    (tenant_id, name, type, base_url, header_name, if_headers, if_queries,
		     secret_hint, secret_nonce, secret_ciphertext)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10)
		RETURNING id::text, tenant_id::text, name, type, base_url, header_name,
		          if_headers, if_queries, secret_hint, secret_nonce, secret_ciphertext,
		          created_at, updated_at`,
		rule.TenantID, rule.Name, rule.Type, rule.BaseURL, rule.HeaderName,
		string(ifHeaders), string(ifQueries), rule.SecretHint, rule.Nonce, rule.Sealed)
	created, err := scanRule(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Rule{}, ErrExists
		}
		return Rule{}, fmt.Errorf("create injection rule: %w", err)
	}
	return created, nil
}

func (s *PgStore) Update(ctx context.Context, rule Rule) (Rule, error) {
	ifHeaders, err := json.Marshal(orEmptyMap(rule.IfHeaders))
	if err != nil {
		return Rule{}, err
	}
	ifQueries, err := json.Marshal(orEmptyMap(rule.IfQueries))
	if err != nil {
		return Rule{}, err
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE injection_rules
		SET name = $3, base_url = $4, header_name = $5, if_headers = $6::jsonb,
		    if_queries = $7::jsonb, secret_hint = $8, secret_nonce = $9,
		    secret_ciphertext = $10, updated_at = now()
		WHERE tenant_id = $1::uuid AND id = $2::uuid
		RETURNING id::text, tenant_id::text, name, type, base_url, header_name,
		          if_headers, if_queries, secret_hint, secret_nonce, secret_ciphertext,
		          created_at, updated_at`,
		rule.TenantID, rule.ID, rule.Name, rule.BaseURL, rule.HeaderName,
		string(ifHeaders), string(ifQueries), rule.SecretHint, rule.Nonce, rule.Sealed)
	updated, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return Rule{}, ErrExists
		}
		return Rule{}, fmt.Errorf("update injection rule: %w", err)
	}
	return updated, nil
}

func (s *PgStore) Delete(ctx context.Context, tenantID, ruleID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM injection_rules WHERE tenant_id = $1::uuid AND id = $2::uuid`,
		tenantID, ruleID)
	if err != nil {
		return fmt.Errorf("delete injection rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) Pool() *pgxpool.Pool { return s.pool }

type scanner interface {
	Scan(dest ...any) error
}

func scanRule(row scanner) (Rule, error) {
	var rule Rule
	var headers, queries []byte
	err := row.Scan(
		&rule.ID, &rule.TenantID, &rule.Name, &rule.Type, &rule.BaseURL, &rule.HeaderName,
		&headers, &queries, &rule.SecretHint, &rule.Nonce, &rule.Sealed,
		&rule.CreatedAt, &rule.UpdatedAt,
	)
	if err != nil {
		return Rule{}, err
	}
	if len(headers) > 0 {
		if err := json.Unmarshal(headers, &rule.IfHeaders); err != nil {
			return Rule{}, fmt.Errorf("decode injection header conditions: %w", err)
		}
	}
	if len(queries) > 0 {
		if err := json.Unmarshal(queries, &rule.IfQueries); err != nil {
			return Rule{}, fmt.Errorf("decode injection query conditions: %w", err)
		}
	}
	return rule, nil
}

func orEmptyMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
