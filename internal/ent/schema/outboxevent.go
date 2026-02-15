package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// OutboxEvent holds the schema definition for the outbox events table.
type OutboxEvent struct {
	ent.Schema
}

// Fields of the OutboxEvent.
func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Tenant identifier"),
		field.String("aggregate_type").
			NotEmpty().
			Comment("Aggregate type (subscription, plan, feature_gate)"),
		field.UUID("aggregate_id", uuid.UUID{}).
			Comment("Aggregate ID"),
		field.String("event_type").
			NotEmpty().
			Comment("Event type (subscription.created, subscription.upgraded, etc.)"),
		field.JSON("payload", map[string]any{}).
			Comment("Event payload as JSON"),
		field.String("status").
			Default("PENDING").
			Comment("Status: PENDING, PUBLISHED, FAILED"),
		field.Int("attempts").
			Default(0).
			Comment("Publish attempt count"),
		field.Time("last_attempt_at").
			Optional().
			Comment("Last publish attempt timestamp"),
		field.Time("published_at").
			Optional().
			Comment("Successful publish timestamp"),
		field.String("error_message").
			Optional().
			Comment("Error message (if failed)"),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Creation timestamp"),
	}
}

// Indexes of the OutboxEvent.
func (OutboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("aggregate_type", "aggregate_id"),
		index.Fields("event_type"),
		index.Fields("status"),
		index.Fields("created_at"),
	}
}
