package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// UsageEvent records a single usage metric reported by any microservice.
// Used for overage calculation and analytics.
type UsageEvent struct {
	ent.Schema
}

// Fields of the UsageEvent.
func (UsageEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Owning tenant"),
		// metric_type identifies the usage dimension:
		// orders_placed, riders_active, sms_sent, storage_gb, api_calls, etc.
		field.String("metric_type").
			NotEmpty().
			MaxLen(64).
			Comment("Usage dimension: orders_placed, sms_sent, riders_active, etc."),
		// service_name identifies the originating microservice.
		field.String("service_name").
			NotEmpty().
			MaxLen(64).
			Comment("Reporting microservice: ordering, logistics, notifications, pos, etc."),
		// value is the numeric quantity for this event.
		field.Float("value").
			Comment("Numeric quantity for this usage event"),
		// period_start / period_end allow batch usage events to carry a time window.
		field.Time("period_start").
			Optional().
			Comment("Start of the usage period (optional, defaults to created_at)"),
		field.Time("period_end").
			Optional().
			Comment("End of the usage period (optional)"),
		// metadata carries additional context (e.g. order_id, rider_id).
		field.JSON("metadata", map[string]any{}).
			Default(map[string]any{}),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Indexes of the UsageEvent.
func (UsageEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("tenant_id", "metric_type"),
		index.Fields("service_name"),
		index.Fields("created_at"),
		index.Fields("tenant_id", "service_name", "metric_type"),
	}
}
