package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/subscription-service/internal/ent"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set), on a throwaway schema.
func newTenantSyncClient(t *testing.T) *ent.Client {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "ts_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", url+sep+"search_path="+schemaName)
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	return client
}

// Auth renamed the tenant (same UUID, new slug). The slug upsert used to hit the primary key
// and fail; the row must now move onto the new slug.
func TestSyncTenantRenamesSlugForSameUUID(t *testing.T) {
	client := newTenantSyncClient(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := client.Tenant.Create().SetID(id).SetName("Old").SetSlug("old-co").Save(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id.String(), "slug": "new-co", "name": "New", "status": "active"})
	}))
	t.Cleanup(srv.Close)

	got, err := NewSyncer(client, srv.URL).SyncTenant(ctx, "new-co")
	if err != nil || got != id {
		t.Fatalf("SyncTenant = %s, %v; want %s", got, err, id)
	}
	row, err := client.Tenant.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Slug != "new-co" || row.Name != "New" {
		t.Fatalf("row = slug %q name %q; want new-co / New", row.Slug, row.Name)
	}
}
