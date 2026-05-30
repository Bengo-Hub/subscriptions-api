package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/overagecharge"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/ent/usageevent"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// OverageService calculates and accumulates daily overage charges for tenants
// that have exceeded their plan limits. Charges are rolled into the renewal invoice.
type OverageService struct {
	log *zap.Logger
	orm *ent.Client
}

// NewOverageService creates a new OverageService.
func NewOverageService(log *zap.Logger, orm *ent.Client) *OverageService {
	return &OverageService{
		log: log.Named("billing.overage"),
		orm: orm,
	}
}

// CalculateDailyOverages calculates overages for all active subscriptions for the given date.
// Should be called once per day, typically at 00:05 UTC.
func (s *OverageService) CalculateDailyOverages(ctx context.Context, date time.Time) error {
	periodDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	periodStart := periodDate
	periodEnd := periodDate.Add(24 * time.Hour)

	subs, err := s.orm.TenantSubscription.Query().
		Where(tenantsubscription.StatusIn(
			tenantsubscription.StatusACTIVE,
			tenantsubscription.StatusTRIAL,
		)).
		WithPlan().
		All(ctx)
	if err != nil {
		return fmt.Errorf("list active subscriptions: %w", err)
	}

	for _, sub := range subs {
		if sub.Edges.Plan == nil || sub.Edges.Plan.TierLimitsJSON == nil {
			continue
		}
		if err := s.processSubscriptionOverages(ctx, sub, periodDate, periodStart, periodEnd); err != nil {
			s.log.Error("overage calculation failed for tenant",
				zap.String("tenant_id", sub.TenantID.String()),
				zap.Error(err),
			)
		}
	}
	return nil
}

// overageMetrics maps plan limit keys to the metric_type names used in usage_events.
var overageMetrics = map[string]struct {
	limitKey    string
	unitPrice   string // key in tierLimitsJSON for per-unit overage price; "" = no overage billing
}{
	"orders":        {limitKey: "max_orders_per_day", unitPrice: "overage_orders_price_per_100_month"},
	"riders_active": {limitKey: "max_riders", unitPrice: "overage_rider_price_per_month"},
	"transactions":  {limitKey: "max_transactions_per_month", unitPrice: ""},
	"products":      {limitKey: "inventory_max_sku", unitPrice: ""},
	"deliveries":    {limitKey: "max_orders_per_day", unitPrice: ""},
}

func (s *OverageService) processSubscriptionOverages(
	ctx context.Context,
	sub *ent.TenantSubscription,
	periodDate, periodStart, periodEnd time.Time,
) error {
	limits := sub.Edges.Plan.TierLimitsJSON

	for metricType, meta := range overageMetrics {
		rawLimit, ok := limits[meta.limitKey]
		if !ok {
			continue
		}
		var planLimit int
		switch v := rawLimit.(type) {
		case float64:
			planLimit = int(v)
		case int:
			planLimit = v
		}
		if planLimit <= 0 { // -1 = unlimited
			continue
		}

		// Sum usage_events for this tenant+metric in the period
		events, err := s.orm.UsageEvent.Query().
			Where(
				usageevent.TenantIDEQ(sub.TenantID),
				usageevent.MetricTypeEQ(metricType),
				usageevent.CreatedAtGTE(periodStart),
				usageevent.CreatedAtLT(periodEnd),
			).
			All(ctx)
		if err != nil {
			return fmt.Errorf("query usage_events for %s: %w", metricType, err)
		}

		var totalUsage float64
		for _, e := range events {
			totalUsage += e.Value
		}

		if totalUsage <= float64(planLimit) {
			continue
		}

		unitsOver := totalUsage - float64(planLimit)

		// Determine unit price
		var unitPriceKes float64
		if meta.unitPrice != "" {
			if v, ok := limits[meta.unitPrice]; ok {
				switch pv := v.(type) {
				case float64:
					unitPriceKes = pv
				case int:
					unitPriceKes = float64(pv)
				}
			}
		}

		totalChargeKes := unitsOver * unitPriceKes

		// Upsert the overage charge (unique index: sub_id + metric_type + period_date)
		existing, err := s.orm.OverageCharge.Query().
			Where(
				overagecharge.TenantSubscriptionIDEQ(sub.ID),
				overagecharge.MetricTypeEQ(metricType),
				overagecharge.PeriodDateEQ(periodDate),
			).
			Only(ctx)
		if err == nil {
			// Update existing record with latest usage values
			if _, err := s.orm.OverageCharge.UpdateOneID(existing.ID).
				SetUnitsUsed(totalUsage).
				SetUnitsOver(unitsOver).
				SetTotalChargeKes(totalChargeKes).
				Save(ctx); err != nil {
				return fmt.Errorf("update overage charge %s: %w", metricType, err)
			}
		} else if ent.IsNotFound(err) {
			if _, err := s.orm.OverageCharge.Create().
				SetTenantSubscriptionID(sub.ID).
				SetTenantID(sub.TenantID).
				SetMetricType(metricType).
				SetPeriodDate(periodDate).
				SetUnitsUsed(totalUsage).
				SetPlanLimit(planLimit).
				SetUnitsOver(unitsOver).
				SetUnitPriceKes(unitPriceKes).
				SetTotalChargeKes(totalChargeKes).
				SetStatus(overagecharge.StatusPending).
				Save(ctx); err != nil {
				return fmt.Errorf("create overage charge %s: %w", metricType, err)
			}
			s.log.Info("overage charge created",
				zap.String("tenant_id", sub.TenantID.String()),
				zap.String("metric", metricType),
				zap.Float64("units_over", unitsOver),
				zap.Float64("total_kes", totalChargeKes),
			)
		} else {
			return fmt.Errorf("query overage charge %s: %w", metricType, err)
		}
	}
	return nil
}

// GetAccumulatedOverage returns the sum of pending overage charges for a tenant in the current period.
func (s *OverageService) GetAccumulatedOverage(ctx context.Context, tenantID uuid.UUID) (float64, error) {
	charges, err := s.orm.OverageCharge.Query().
		Where(
			overagecharge.TenantIDEQ(tenantID),
			overagecharge.StatusEQ(overagecharge.StatusPending),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query overage charges: %w", err)
	}

	var total float64
	for _, c := range charges {
		total += c.TotalChargeKes
	}
	return total, nil
}

// MarkInvoiced marks overage charges as invoiced after a renewal payment succeeds.
func (s *OverageService) MarkInvoiced(ctx context.Context, tenantID uuid.UUID) error {
	now := time.Now()
	if err := s.orm.OverageCharge.Update().
		Where(
			overagecharge.TenantIDEQ(tenantID),
			overagecharge.StatusEQ(overagecharge.StatusPending),
		).
		SetStatus(overagecharge.StatusInvoiced).
		SetNillableInvoicedAt(&now).
		Exec(ctx); err != nil {
		return fmt.Errorf("mark overages invoiced: %w", err)
	}
	return nil
}

// WaiveOverage allows a platform admin to waive a specific overage charge.
func (s *OverageService) WaiveOverage(ctx context.Context, overageID uuid.UUID) error {
	if err := s.orm.OverageCharge.UpdateOneID(overageID).
		SetStatus(overagecharge.StatusWaived).
		Exec(ctx); err != nil {
		return fmt.Errorf("waive overage %s: %w", overageID, err)
	}
	return nil
}

// ListPendingByTenant lists pending overage charges for a tenant with breakdown.
func (s *OverageService) ListPendingByTenant(ctx context.Context, tenantID uuid.UUID) ([]*ent.OverageCharge, error) {
	return s.orm.OverageCharge.Query().
		Where(
			overagecharge.TenantIDEQ(tenantID),
			overagecharge.StatusEQ(overagecharge.StatusPending),
		).
		Order(ent.Asc("period_date")).
		All(ctx)
}
