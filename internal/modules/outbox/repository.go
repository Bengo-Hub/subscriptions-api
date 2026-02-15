package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	events "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
)

// Repository implements OutboxRepository using Ent ORM.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new outbox repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// BeginTx starts a database transaction.
func (r *Repository) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
}

// CreateOutboxRecord stores an event in the outbox within a transaction.
func (r *Repository) CreateOutboxRecord(ctx context.Context, tx *sql.Tx, record *events.OutboxRecord) error {
	query := `
		INSERT INTO outbox_events (
			id, tenant_id, aggregate_type, aggregate_id, event_type,
			payload, status, attempts, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := tx.ExecContext(ctx, query,
		record.ID,
		record.TenantID,
		record.AggregateType,
		record.AggregateID,
		record.EventType,
		record.Payload,
		record.Status,
		record.Attempts,
		record.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("insert outbox record: %w", err)
	}

	return nil
}

// GetPendingRecords fetches pending events for publishing.
func (r *Repository) GetPendingRecords(ctx context.Context, limit int) ([]*events.OutboxRecord, error) {
	query := `
		SELECT 
			id, tenant_id, aggregate_type, aggregate_id, event_type,
			payload, status, attempts, last_attempt_at, published_at,
			error_message, created_at
		FROM outbox_events
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, events.StatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending records: %w", err)
	}
	defer rows.Close()

	var records []*events.OutboxRecord
	for rows.Next() {
		record := &events.OutboxRecord{}
		var lastAttemptAt, publishedAt sql.NullTime
		var errorMessage sql.NullString

		err := rows.Scan(
			&record.ID,
			&record.TenantID,
			&record.AggregateType,
			&record.AggregateID,
			&record.EventType,
			&record.Payload,
			&record.Status,
			&record.Attempts,
			&lastAttemptAt,
			&publishedAt,
			&errorMessage,
			&record.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan record: %w", err)
		}

		if lastAttemptAt.Valid {
			record.LastAttemptAt = &lastAttemptAt.Time
		}
		if publishedAt.Valid {
			record.PublishedAt = &publishedAt.Time
		}
		if errorMessage.Valid {
			record.ErrorMessage = &errorMessage.String
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return records, nil
}

// MarkAsPublished marks an event as successfully published.
func (r *Repository) MarkAsPublished(ctx context.Context, id uuid.UUID, publishedAt time.Time) error {
	query := `
		UPDATE outbox_events
		SET status = $1, published_at = $2
		WHERE id = $3
	`

	result, err := r.db.ExecContext(ctx, query, events.StatusPublished, publishedAt, id)
	if err != nil {
		return fmt.Errorf("update outbox record: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("event not found: %s", id)
	}

	return nil
}

// MarkAsFailed marks an event as failed and increments attempts.
func (r *Repository) MarkAsFailed(ctx context.Context, id uuid.UUID, errorMessage string, lastAttemptAt time.Time) error {
	query := `
		UPDATE outbox_events
		SET status = $1, attempts = attempts + 1, last_attempt_at = $2, error_message = $3
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, events.StatusPending, lastAttemptAt, errorMessage, id)
	if err != nil {
		return fmt.Errorf("update outbox record: %w", err)
	}

	return nil
}
