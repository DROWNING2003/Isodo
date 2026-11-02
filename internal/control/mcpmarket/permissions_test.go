package mcpmarket

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMCPRuntimeRolePermissions(t *testing.T) {
	database := os.Getenv("ISODO_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable migrated PostgreSQL")
	}
	pool, err := pgxpool.New(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	role := "mcp_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{role}.Sanitize()
	if _, err = pool.Exec(t.Context(), "CREATE ROLE "+identifier+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DROP OWNED BY "+identifier+"; DROP ROLE "+identifier)
	source, err := os.ReadFile("../../../deploy/sql/grant-mcp-market.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := []string{}
	for line := range strings.SplitSeq(string(source), "\n") {
		if strings.HasPrefix(line, "\\") {
			continue
		}
		sql = append(sql, strings.ReplaceAll(line, `:"app_role"`, identifier))
	}
	if _, err = pool.Exec(t.Context(), strings.Join(sql, "\n")); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE "+identifier); err != nil {
		t.Fatal(err)
	}
	var name string
	if err = tx.QueryRow(t.Context(), `SELECT name FROM mcp_catalog WHERE market_mcp_id='github'`).Scan(&name); err != nil || name != "GitHub" {
		t.Fatalf("runtime cannot load market: %s %v", name, err)
	}
	var catalogWrite bool
	if err = tx.QueryRow(t.Context(), `SELECT has_table_privilege(current_user,'mcp_catalog','INSERT')`).Scan(&catalogWrite); err != nil || catalogWrite {
		t.Fatal("runtime can mutate catalog")
	}
	for _, table := range []string{"mcp_favorites", "mcp_oauth_connections", "mcp_oauth_transactions", "mcp_sandbox_bindings"} {
		for _, action := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			var allowed bool
			if err = tx.QueryRow(t.Context(), `SELECT has_table_privilege(current_user,$1,$2)`, table, action).Scan(&allowed); err != nil || !allowed {
				t.Fatalf("missing %s on %s: %v", action, table, err)
			}
		}
	}
}
