package migrate

import (
	"context"
	"database/sql"
	"fmt"
)

// legacyIndexes are indexes removed from the ent schema. The online migrator
// (Schema.Create with the versioned dir) runs without WithDropIndex, so it never drops
// an index on its own; DropLegacyIndexes removes them explicitly. Idempotent.
var legacyIndexes = []string{
	// Superseded by supportfeecycle_agreement_id_cycle_number (support agreements,
	// 2026-10-04). Left in place it rejects the first cycle of a second agreement.
	"supportfeecycle_tenant_subscription_id_cycle_number",
	// Superseded by the (tenant_id, status, due_date) and (status, due_date) composites.
	"supportfeecycle_tenant_id",
	"supportfeecycle_status",
	"supportfeecycle_due_date",
}

// DropLegacyIndexes drops indexes that no longer exist in the ent schema. Run it after
// Schema.Create, on the same connection that holds the migration advisory lock.
func DropLegacyIndexes(ctx context.Context, db *sql.DB) error {
	for _, name := range legacyIndexes {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS %q`, name)); err != nil {
			return fmt.Errorf("drop legacy index %s: %w", name, err)
		}
	}
	return nil
}
