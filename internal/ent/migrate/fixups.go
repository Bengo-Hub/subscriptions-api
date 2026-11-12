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

// whatsAppCategoryMoveFile moves legacy "whatsapp" bundle balances into the per-category wallets
// (owner, 2026-11-12). Schema.Create with WithDir inspects and diffs the live schema; it never
// executes the versioned .sql files, so a data move written only as a .sql file would not reach
// production. MoveWhatsAppToCategories runs this file itself.
const whatsAppCategoryMoveFile = "migrations/20261112090000_whatsapp_category_credits.sql"

// MoveWhatsAppToCategories runs the WhatsApp category balance move in one transaction. Idempotent:
// an emptied legacy wallet is skipped and every ledger row has its own key, so running it on every
// migrate is safe. Run it after Schema.Create, under the migration advisory lock.
func MoveWhatsAppToCategories(ctx context.Context, db *sql.DB) error {
	stmt, err := migrations.ReadFile(whatsAppCategoryMoveFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", whatsAppCategoryMoveFile, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("whatsapp category move: begin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, string(stmt)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("whatsapp category move: %w", err)
	}
	return tx.Commit()
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
