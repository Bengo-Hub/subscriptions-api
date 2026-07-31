package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	enttenant "github.com/bengobox/subscription-service/internal/ent/tenant"
)

// Syncer handles dynamic syncing of tenant data from auth-api using Ent ORM.
type Syncer struct {
	client  *ent.Client
	authURL string
	db      *sql.DB
}

// NewSyncer creates a new TenantSyncer.
func NewSyncer(client *ent.Client, authURL string) *Syncer {
	return &Syncer{
		client:  client,
		authURL: authURL,
	}
}

// WithDB attaches the raw database handle the drift self-heal needs. Without it a detected
// tenant-UUID drift is reported but not repaired (the stale local UUID is still returned),
// because re-keying spans every tenant-scoped table and cannot be expressed through Ent.
func (s *Syncer) WithDB(db *sql.DB) *Syncer {
	s.db = db
	return s
}

// sqlLiteral renders s as a single-quoted Postgres string literal, doubling embedded quotes.
func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// adoptTenantIDSQL re-keys every tenant-scoped row from oldID onto newID in one transaction.
// Ported from the equivalent fix in pos-api/inventory-api ([[reference_tenant_uuid_drift]]):
// subscriptions-api's own tenant cache can drift from auth-api's live UUID the same way theirs
// did, and here that drift is the confirmed root cause of referral/equity payouts silently
// excluding a tenant's subscription revenue (a stale UUID gets stamped into
// invoice.metadata.billed_tenant_id at generation time in billing/invoice_service.go).
//
// Columns are discovered from the live catalog: any column literally named tenant_id, plus any
// column with a real FK to tenants(id) — Ent names one-to-one edge columns after the edge, not
// the entity, so a name-only sweep misses those and the final DELETE then fails on the FK.
//
// The clone lands on a throwaway slug so the unique(slug) index does not fire mid-transaction;
// the real slug is restored once the stale row is gone. Where a unique index makes a rewrite
// impossible (a row already exists under the new UUID with the same key), the stale duplicate
// is dropped instead.
const adoptTenantIDSQL = `
DO $$
DECLARE
  old_id text := %s;
  new_id text := %s;
  slug_v text := %s;
  r record;
  cols text;
  vals text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id::text = old_id) THEN RETURN; END IF;

  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id::text = new_id) THEN
    SELECT string_agg(quote_ident(column_name), ', ' ORDER BY ordinal_position) INTO cols
      FROM information_schema.columns
     WHERE table_schema='public' AND table_name='tenants';
    SELECT string_agg(CASE WHEN column_name='id'   THEN quote_literal(new_id)
                           WHEN column_name='slug' THEN quote_literal('__drift_migrating__')
                           ELSE quote_ident(column_name) END, ', ' ORDER BY ordinal_position) INTO vals
      FROM information_schema.columns
     WHERE table_schema='public' AND table_name='tenants';
    EXECUTE format('INSERT INTO tenants (%%s) SELECT %%s FROM tenants WHERE id::text = %%L', cols, vals, old_id);
  END IF;

  FOR r IN
    SELECT c.table_name AS tbl, c.column_name AS col
      FROM information_schema.columns c
      JOIN information_schema.tables t
        ON t.table_schema=c.table_schema AND t.table_name=c.table_name AND t.table_type='BASE TABLE'
     WHERE c.table_schema='public' AND c.column_name='tenant_id'
    UNION
    SELECT con.conrelid::regclass::text, a.attname
      FROM pg_constraint con
      JOIN unnest(con.conkey) k ON true
      JOIN pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=k
     WHERE con.confrelid='tenants'::regclass AND con.contype='f'
  LOOP
    BEGIN
      EXECUTE format('UPDATE %%I SET %%I = %%L WHERE %%I = %%L', r.tbl, r.col, new_id, r.col, old_id);
    EXCEPTION WHEN unique_violation OR foreign_key_violation THEN
      EXECUTE format('DELETE FROM %%I WHERE %%I = %%L', r.tbl, r.col, old_id);
      RAISE WARNING 'tenant drift: dropped stale rows from %% (collided under new UUID)', r.tbl;
    END;
  END LOOP;

  EXECUTE format('DELETE FROM tenants WHERE id::text = %%L', old_id);
  EXECUTE format('UPDATE tenants SET slug = %%L WHERE id::text = %%L', slug_v, new_id);
END $$;`

// adoptAuthTenantID re-keys the locally projected tenant onto the UUID auth-api reports, then
// returns that UUID. When no raw DB handle is wired up it degrades to a loud warning and keeps
// returning the local UUID — a partial rewrite would be worse than none.
func (s *Syncer) adoptAuthTenantID(ctx context.Context, localID, remoteID uuid.UUID, slug, name string) (uuid.UUID, error) {
	if s.db == nil {
		log.Printf("  [tenant-sync] cannot self-heal drift for %s: no DB handle wired (still using local %s)", slug, localID)
		return localID, nil
	}

	// A DO block takes no bind parameters, so the three values are inlined as quoted SQL
	// literals. localID/remoteID are already-parsed UUIDs; slug is quoted defensively.
	stmt := fmt.Sprintf(adoptTenantIDSQL,
		sqlLiteral(localID.String()), sqlLiteral(remoteID.String()), sqlLiteral(slug))
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		log.Printf("  [tenant-sync] self-heal for %s FAILED (still using local %s): %v", slug, localID, err)
		return localID, nil
	}

	upd := s.client.Tenant.UpdateOneID(remoteID).SetSyncStatus("synced").SetLastSyncAt(time.Now())
	if n := strings.TrimSpace(name); n != "" {
		upd = upd.SetName(n)
	}
	if err := upd.Exec(ctx); err != nil {
		log.Printf("  [tenant-sync] post-heal metadata refresh for %s failed: %v", slug, err)
	}

	log.Printf("  [tenant-sync] self-healed %s: %s -> %s", slug, localID, remoteID)
	return remoteID, nil
}

// fetchAuthTenant fetches the tenant record directly from auth-api by slug (no cache layer in
// this service). Returns an error if unreachable/not found — callers treat that as "skip the
// drift check", never as grounds to change the locally cached UUID.
func (s *Syncer) fetchAuthTenant(slug string) (authAPITenantResponse, error) {
	endpoint := strings.TrimRight(s.authURL, "/") + "/api/v1/tenants/by-slug/" + slug
	resp, err := http.Get(endpoint) //nolint:noctx
	if err != nil {
		return authAPITenantResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return authAPITenantResponse{}, fmt.Errorf("auth-api HTTP %d for %q", resp.StatusCode, slug)
	}
	var remote authAPITenantResponse
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return authAPITenantResponse{}, err
	}
	return remote, nil
}

// authAPITenantResponse is the minimal tenant JSON response from GET /api/v1/tenants/by-slug/{slug}.
// Only fields that this service persists locally are included.
type authAPITenantResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Status  string `json:"status"`
	UseCase string `json:"use_case"`
}

// SyncTenant fetches the tenant record from auth-api and persists the minimal
// reference in the local PG DB using Ent.
func (s *Syncer) SyncTenant(ctx context.Context, slug string) (uuid.UUID, error) {
	// Fast path: check if tenant exists locally.
	existing, err := s.client.Tenant.Query().Where(enttenant.SlugEQ(slug)).Only(ctx)
	if err == nil && existing != nil {
		// Drift guard: auth-api owns the tenant UUID. Returning the locally cached UUID forever
		// silently forks the tenant the moment auth-api's live UUID for this slug diverges —
		// this is the confirmed cause of a referral/equity payout excluding a linked tenant's
		// subscription revenue, since this service stamps its own tenant UUID into
		// invoice.metadata.billed_tenant_id when generating subscription invoices (see
		// billing/invoice_service.go). Report the mismatch and let auth win.
		if remote, fErr := s.fetchAuthTenant(slug); fErr == nil {
			if remoteID, pErr := uuid.Parse(strings.TrimSpace(remote.ID)); pErr == nil && remoteID != uuid.Nil && remoteID != existing.ID {
				log.Printf("  [tenant-sync] DRIFT: %s is %s locally but %s in auth-api — adopting auth's UUID", slug, existing.ID, remoteID)
				return s.adoptAuthTenantID(ctx, existing.ID, remoteID, slug, remote.Name)
			}
		}
		return existing.ID, nil
	}

	endpoint := strings.TrimRight(s.authURL, "/") + "/api/v1/tenants/by-slug/" + slug

	log.Printf("  [tenant-sync] dynamically fetching %s from %s", slug, endpoint)
	resp, err := http.Get(endpoint) //nolint:noctx
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: tenant %q not found (404)", slug)
	}
	if resp.StatusCode != http.StatusOK {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: auth-api HTTP %d for %q", resp.StatusCode, slug)
	}

	var remote authAPITenantResponse
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: decode response: %w", err)
	}
	realID, err := uuid.Parse(remote.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: invalid UUID %q: %w", remote.ID, err)
	}

	now := time.Now()

	// Use Ent Upsert with explicit conflict target (required by PostgreSQL 15+)
	err = s.client.Tenant.Create().
		SetID(realID).
		SetName(remote.Name).
		SetSlug(remote.Slug).
		SetStatus(remote.Status).
		SetNillableUseCase(nillableStr(remote.UseCase)).
		SetSyncStatus("synced").
		SetLastSyncAt(now).
		OnConflictColumns(enttenant.FieldSlug).
		UpdateNewValues().
		Exec(ctx)

	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant.Syncer: upsert failed: %w", err)
	}

	log.Printf("  [tenant-sync] dynamically synced %s (UUID %s) into subscriptions-api DB", slug, realID)
	return realID, nil
}

// nillableStr returns a *string if non-empty, nil otherwise.
func nillableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
