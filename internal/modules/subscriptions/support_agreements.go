package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/supportagreement"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Support agreements: the recurring hosting and support fee of one-time-license tenants
// (STANDARD) plus any tenant-specific special support the platform owner agrees (SPECIAL). See
// the SupportAgreement schema for the model and support_schedule.go for the period math. This
// file is the single place that creates, reschedules, bills and settles them; the jobs, admin
// handlers and the payment consumer all call into it.

// maxCycleCatchUp bounds how many missed periods one generation pass bills for a single
// agreement (a monthly agreement after a two-year outage, say). The next pass continues.
const maxCycleCatchUp = 24

// ErrSupportValidation marks a caller error (bad input or a disallowed transition).
var ErrSupportValidation = errors.New("invalid support agreement request")

// MetaCollection is the agreement metadata key marking a personal collection (CollectionPersonal).
const (
	MetaCollection     = "collection"
	CollectionPersonal = "personal"
)

// IsPersonalAgreement reports an agreement collected personally (off the company's books).
func IsPersonalAgreement(meta map[string]any) bool {
	v, _ := meta[MetaCollection].(string)
	return v == CollectionPersonal
}

func parseCollection(v string) (personal bool, err error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "business":
		return false, nil
	case CollectionPersonal:
		return true, nil
	}
	return false, supportErr("collection must be business or personal")
}

func supportErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrSupportValidation, fmt.Sprintf(format, a...))
}

// payableSupportStatuses are the cycle states that still owe money.
var payableSupportStatuses = []supportfeecycle.Status{
	supportfeecycle.StatusPENDING,
	supportfeecycle.StatusINVOICED,
	supportfeecycle.StatusOVERDUE,
}

// IsPerpetualLicense selects subscriptions whose PLAN is a one-time license (the subscription row
// itself may carry a stale MONTHLY default, so the plan is authoritative).
func IsPerpetualLicense() func(*ent.TenantSubscriptionQuery) {
	return func(q *ent.TenantSubscriptionQuery) {
		q.Where(tenantsubscription.HasPlanWith(subscriptionplan.BillingCycleEQ("ONE_TIME")))
	}
}

// SupportPlanCodeFor derives the SUPPORT_* catalog code from a one-time license plan code:
//
//	POWERSUITE_DUKA_GOLD_ONE_TIME -> SUPPORT_DUKA_GOLD
//	LIBRARY_PROFESSIONAL_ONE_TIME -> SUPPORT_LIBRARY_PROFESSIONAL
//	ERP_STARTER_ONE_TIME          -> SUPPORT_ERP_STARTER
//	AFYA_CLINIC_ONE_TIME          -> SUPPORT_AFYA_CLINIC
//
// ok=false for a plan code that is not a one-time license.
func SupportPlanCodeFor(licensePlanCode string) (code string, ok bool) {
	if !strings.HasSuffix(licensePlanCode, "_ONE_TIME") {
		return "", false
	}
	base := strings.TrimPrefix(strings.TrimSuffix(licensePlanCode, "_ONE_TIME"), "POWERSUITE_")
	return "SUPPORT_" + base, true
}

// AgreementInterval returns an agreement's period length. Stored rows were validated on write, so
// a failure here only happens on hand-edited data; it falls back to annual.
func AgreementInterval(a *ent.SupportAgreement) SupportInterval {
	iv, err := ResolveSupportInterval(a.BillingCycle, a.IntervalCount, string(a.IntervalUnit))
	if err != nil {
		return SupportInterval{Count: 12, Unit: supportagreement.IntervalUnitMONTH}
	}
	return iv
}

// AgreementPeriodAmount is the charge for one period: the agreed amount when set, otherwise the
// support plan's annual price prorated to the period length. plan may be nil for SPECIAL ones.
func AgreementPeriodAmount(a *ent.SupportAgreement, plan *ent.SubscriptionPlan) float64 {
	if a.Amount != nil {
		return *a.Amount
	}
	if plan == nil {
		return 0
	}
	return ProrateAnnualSupport(plan.BasePrice, AgreementInterval(a))
}

// scheduleAnchor returns the anchor and the cycle_count it was set at. Falls back to starts_at
// and 0, which is exact for any agreement that was never rescheduled.
func scheduleAnchor(a *ent.SupportAgreement) (time.Time, int) {
	anchor := a.StartsAt
	cycle := 0
	if a.Metadata != nil {
		if v, ok := a.Metadata[MetaScheduleAnchor].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				anchor = t
			}
		}
		switch n := a.Metadata[MetaScheduleAnchorCycle].(type) {
		case float64:
			cycle = int(n)
		case int:
			cycle = n
		}
	}
	return anchor.UTC(), cycle
}

func withAnchor(meta map[string]any, anchor time.Time, cycle int) map[string]any {
	out := make(map[string]any, len(meta)+2)
	for k, v := range meta {
		out[k] = v
	}
	out[MetaScheduleAnchor] = anchor.UTC().Format(time.RFC3339Nano)
	out[MetaScheduleAnchorCycle] = cycle
	return out
}

// ── Enrollment ──────────────────────────────────────────────────────────────

// EnsureStandardSupportAgreements creates the STANDARD agreement for every ACTIVE one-time
// license that lacks one, linking any cycles generated before agreements existed. After the first
// run the query returns nothing, so this stays cheap however many tenants exist.
func (s *Service) EnsureStandardSupportAgreements(ctx context.Context, now time.Time) (int, error) {
	subs, err := s.client.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.HasPlanWith(subscriptionplan.BillingCycleEQ("ONE_TIME")),
			tenantsubscription.Not(tenantsubscription.HasSupportAgreementsWith(supportagreement.KindEQ(supportagreement.KindSTANDARD))),
		).
		WithPlan().
		WithSupportFeeCycles(func(q *ent.SupportFeeCycleQuery) {
			q.Where(supportfeecycle.AgreementIDIsNil())
		}).
		All(ctx)
	if err != nil || len(subs) == 0 {
		return 0, err
	}
	codes := make([]string, 0, len(subs))
	for _, sub := range subs {
		if sub.Edges.Plan != nil {
			if c, ok := SupportPlanCodeFor(sub.Edges.Plan.PlanCode); ok {
				codes = append(codes, c)
			}
		}
	}
	plans, err := s.client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeIn(codes...)).All(ctx)
	if err != nil {
		return 0, err
	}
	byCode := make(map[string]*ent.SubscriptionPlan, len(plans))
	for _, p := range plans {
		byCode[p.PlanCode] = p
	}

	annual := SupportInterval{Count: 12, Unit: supportagreement.IntervalUnitMONTH}
	created := 0
	for _, sub := range subs {
		if sub.Edges.Plan == nil {
			continue
		}
		code, ok := SupportPlanCodeFor(sub.Edges.Plan.PlanCode)
		if !ok {
			continue
		}
		plan := byCode[code]
		if plan == nil {
			s.log.Warn("support enrollment: no catalog row for derived support plan",
				zap.String("license_plan", sub.Edges.Plan.PlanCode), zap.String("support_plan", code))
			continue
		}
		// The schedule is anchored on the real onboarding date (created_at is immutable, so a
		// later plan change does not move it). Legacy cycles carry the same numbering
		// (cycle n covers anchor+(n-1)y to anchor+ny), so the agreement continues after them.
		anchor := sub.CreatedAt.UTC()
		cycleCount := PeriodIndexAt(anchor, now, annual)
		for _, c := range sub.Edges.SupportFeeCycles {
			if c.CycleNumber > cycleCount {
				cycleCount = c.CycleNumber
			}
		}
		next := SchedulePeriodStart(anchor, cycleCount, annual)

		if err := s.withTx(ctx, func(tx *ent.Tx) error {
			a, err := tx.SupportAgreement.Create().
				SetTenantID(sub.TenantID).
				SetTenantSubscriptionID(sub.ID).
				SetSupportPlanID(plan.ID).
				SetKind(supportagreement.KindSTANDARD).
				SetName("Hosting and support").
				SetBillingCycle(supportagreement.BillingCycleANNUAL).
				SetIntervalCount(12).
				SetIntervalUnit(supportagreement.IntervalUnitMONTH).
				SetCurrency(currencyOr(plan.Currency)).
				SetBillingTiming(supportagreement.BillingTimingARREARS).
				SetStartsAt(anchor).
				SetNextPeriodStart(next).
				SetCycleCount(cycleCount).
				SetMetadata(withAnchor(nil, anchor, 0)).
				Save(ctx)
			if err != nil {
				return err
			}
			for _, c := range sub.Edges.SupportFeeCycles {
				if err := tx.SupportFeeCycle.UpdateOneID(c.ID).
					SetAgreementID(a.ID).
					SetPeriodEnd(c.DueDate).
					Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			s.log.Error("support enrollment: create standard agreement failed",
				zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		created++
	}
	return created, nil
}

// GenerateDueSupportCycles bills every ACTIVE agreement whose next period is inside the invoice
// lead window. Uses the (status, next_period_start) index, so the scan only touches agreements
// that actually have work.
func (s *Service) GenerateDueSupportCycles(ctx context.Context, now time.Time) (int, error) {
	agreements, err := s.client.SupportAgreement.Query().
		Where(
			supportagreement.StatusEQ(supportagreement.StatusACTIVE),
			supportagreement.NextPeriodStartLTE(now.AddDate(0, 0, SupportFeeInvoiceLeadDays)),
			supportagreement.HasTenantSubscriptionWith(tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE)),
		).
		WithSupportPlan().
		All(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, a := range agreements {
		n, err := s.generateAgreementCycles(ctx, a, now)
		total += n
		if err != nil {
			s.log.Error("support cycles: generation failed",
				zap.String("agreement_id", a.ID.String()), zap.Error(err))
		}
	}
	return total, nil
}

// generateAgreementCycles creates the cycles of one agreement that are due to exist at now.
// Concurrency-safe: the agreement advance is conditional on the cycle_count it read, and the
// (agreement_id, cycle_number) unique index rejects a duplicate cycle.
func (s *Service) generateAgreementCycles(ctx context.Context, a *ent.SupportAgreement, now time.Time) (int, error) {
	iv := AgreementInterval(a)
	anchor, anchorCycle := scheduleAnchor(a)
	created := 0
	for i := 0; i < maxCycleCatchUp && a.Status == supportagreement.StatusACTIVE; i++ {
		n := a.CycleCount + 1
		k := n - 1 - anchorCycle
		start := SchedulePeriodStart(anchor, k, iv)
		if a.EndsAt != nil && !start.Before(*a.EndsAt) {
			if err := s.client.SupportAgreement.UpdateOneID(a.ID).
				SetStatus(supportagreement.StatusENDED).Exec(ctx); err != nil {
				return created, err
			}
			a.Status = supportagreement.StatusENDED
			break
		}
		end := SchedulePeriodStart(anchor, k+1, iv)
		due := SupportDueDate(start, end, a.BillingTiming)
		if SupportCycleCreateAt(start, due).After(now) {
			break
		}
		due = SupportBackdatedDue(due, now)
		amount := AgreementPeriodAmount(a, a.Edges.SupportPlan)

		err := s.withTx(ctx, func(tx *ent.Tx) error {
			moved, err := tx.SupportAgreement.Update().
				Where(supportagreement.IDEQ(a.ID), supportagreement.CycleCountEQ(a.CycleCount)).
				SetCycleCount(n).
				SetNextPeriodStart(end).
				Save(ctx)
			if err != nil {
				return err
			}
			if moved == 0 {
				return errAgreementMoved
			}
			c := tx.SupportFeeCycle.Create().
				SetTenantID(a.TenantID).
				SetTenantSubscriptionID(a.TenantSubscriptionID).
				SetAgreementID(a.ID).
				SetNillableSupportPlanID(a.SupportPlanID).
				SetAnchorDate(anchor).
				SetCycleNumber(n).
				SetPeriodStart(start).
				SetPeriodEnd(end).
				SetDueDate(due).
				SetBasePrice(amount)
			if amount <= 0 {
				// Nothing to collect (a fully discounted agreement): record the period so the
				// history is complete, but never let it block anyone.
				c = c.SetStatus(supportfeecycle.StatusWAIVED).
					SetMetadata(map[string]any{"waive_reason": "zero amount"})
			}
			return c.Exec(ctx)
		})
		if errors.Is(err, errAgreementMoved) {
			return created, nil // another writer advanced it; the next pass re-reads
		}
		if err != nil {
			return created, err
		}
		a.CycleCount = n
		a.NextPeriodStart = end
		created++
		s.log.Info("support cycle created",
			zap.String("tenant_id", a.TenantID.String()), zap.String("agreement", a.Name),
			zap.Int("cycle_number", n), zap.Time("due_date", due), zap.Float64("amount", amount))
	}
	return created, nil
}

var errAgreementMoved = errors.New("agreement advanced concurrently")

// agreementSetters collects field changes so they can be applied inside the update transaction.
type agreementSetters struct {
	fns []func(*ent.SupportAgreementUpdateOne)
}

func (a *agreementSetters) add(fn func(*ent.SupportAgreementUpdateOne)) { a.fns = append(a.fns, fn) }

// ── Admin create / update ───────────────────────────────────────────────────

// SupportAgreementInput carries a create or partial update. Nil pointers leave a field unchanged.
type SupportAgreementInput struct {
	Kind          string     `json:"kind"`
	Name          *string    `json:"name"`
	BillingCycle  *string    `json:"billing_cycle"`
	IntervalCount *int       `json:"interval_count"`
	IntervalUnit  *string    `json:"interval_unit"`
	Amount        *float64   `json:"amount"`
	ClearAmount   bool       `json:"clear_amount"`
	Currency      *string    `json:"currency"`
	BillingTiming *string    `json:"billing_timing"`
	StartsAt      *time.Time `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at"`
	ClearEndsAt   bool       `json:"clear_ends_at"`
	Status        *string    `json:"status"`
	Notes         *string    `json:"notes"`
	BillingEmail  *string    `json:"billing_email"`
	// Collection is "business" (default) or "personal": a personal agreement is the platform
	// owner's own engagement, collected into the owner's personal PayHero channel and kept off
	// the company's books (its invoices carry off_books in treasury).
	Collection *string `json:"collection"`
	// RescheduleFrom applies a cycle or timing change: "next_period" (default) keeps the current
	// period as billed and switches from the next one; "now" cancels the not-yet-invoiced current
	// period and starts the new schedule today.
	RescheduleFrom string `json:"reschedule_from"`
}

// CreateSupportAgreement adds an agreement for a tenant. SPECIAL agreements need an agreed
// amount; a STANDARD one is only allowed for a one-time license without one.
func (s *Service) CreateSupportAgreement(ctx context.Context, tenantID uuid.UUID, in SupportAgreementInput, actor uuid.UUID) (*ent.SupportAgreement, error) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, supportErr("tenant has no subscription")
		}
		return nil, err
	}

	kind := supportagreement.Kind(strings.ToUpper(strings.TrimSpace(in.Kind)))
	if kind == "" {
		kind = supportagreement.KindSPECIAL
	}
	if err := supportagreement.KindValidator(kind); err != nil {
		return nil, supportErr("kind must be STANDARD or SPECIAL")
	}

	cycle := supportagreement.BillingCycleANNUAL
	if in.BillingCycle != nil {
		if cycle, err = NormalizeSupportCycle(*in.BillingCycle); err != nil || cycle == "" {
			return nil, supportErr("billing_cycle is invalid")
		}
	} else if kind == supportagreement.KindSPECIAL {
		cycle = supportagreement.BillingCycleMONTHLY
	}
	iv, err := ResolveSupportInterval(cycle, derefInt(in.IntervalCount), derefStr(in.IntervalUnit))
	if err != nil {
		return nil, supportErr("%s", err.Error())
	}

	timing := supportagreement.BillingTimingADVANCE
	if kind == supportagreement.KindSTANDARD {
		timing = supportagreement.BillingTimingARREARS
	}
	if in.BillingTiming != nil {
		timing = supportagreement.BillingTiming(strings.ToUpper(strings.TrimSpace(*in.BillingTiming)))
		if err := supportagreement.BillingTimingValidator(timing); err != nil {
			return nil, supportErr("billing_timing must be ADVANCE or ARREARS")
		}
	}

	var supportPlan *ent.SubscriptionPlan
	name := strings.TrimSpace(derefStr(in.Name))
	switch kind {
	case supportagreement.KindSTANDARD:
		if sub.Edges.Plan == nil {
			return nil, supportErr("subscription has no plan")
		}
		code, ok := SupportPlanCodeFor(sub.Edges.Plan.PlanCode)
		if !ok {
			return nil, supportErr("standard support applies to one-time licenses only")
		}
		exists, err := s.client.SupportAgreement.Query().Where(
			supportagreement.TenantSubscriptionIDEQ(sub.ID),
			supportagreement.KindEQ(supportagreement.KindSTANDARD),
		).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, supportErr("tenant already has a standard support agreement; update it instead")
		}
		supportPlan, err = s.client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(code)).Only(ctx)
		if err != nil {
			return nil, supportErr("support plan %s not found", code)
		}
		if name == "" {
			name = "Hosting and support"
		}
	case supportagreement.KindSPECIAL:
		if in.Amount == nil || *in.Amount <= 0 {
			return nil, supportErr("amount is required for a special support agreement")
		}
		if name == "" {
			return nil, supportErr("name is required for a special support agreement")
		}
	}
	if in.Amount != nil && *in.Amount < 0 {
		return nil, supportErr("amount must not be negative")
	}

	now := time.Now().UTC()
	startsAt := now
	if in.StartsAt != nil {
		startsAt = in.StartsAt.UTC()
	}
	if in.EndsAt != nil && !in.EndsAt.After(startsAt) {
		return nil, supportErr("ends_at must be after starts_at")
	}
	currency := "KES"
	if supportPlan != nil {
		currency = currencyOr(supportPlan.Currency)
	}
	if in.Currency != nil && strings.TrimSpace(*in.Currency) != "" {
		currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	meta := withAnchor(nil, startsAt, 0)
	if in.BillingEmail != nil && strings.TrimSpace(*in.BillingEmail) != "" {
		meta["billing_email"] = strings.TrimSpace(*in.BillingEmail)
	}
	if in.Collection != nil {
		personal, err := parseCollection(*in.Collection)
		if err != nil {
			return nil, err
		}
		if personal {
			meta[MetaCollection] = CollectionPersonal
		}
	}

	create := s.client.SupportAgreement.Create().
		SetTenantID(tenantID).
		SetTenantSubscriptionID(sub.ID).
		SetKind(kind).
		SetName(name).
		SetBillingCycle(cycle).
		SetIntervalCount(iv.Count).
		SetIntervalUnit(iv.Unit).
		SetNillableAmount(in.Amount).
		SetCurrency(currency).
		SetBillingTiming(timing).
		SetStartsAt(startsAt).
		SetNillableEndsAt(in.EndsAt).
		SetNextPeriodStart(startsAt).
		SetNillableNotes(in.Notes).
		SetMetadata(meta)
	if supportPlan != nil {
		create = create.SetSupportPlanID(supportPlan.ID)
	}
	if actor != uuid.Nil {
		create = create.SetCreatedBy(actor)
	}
	a, err := create.Save(ctx)
	if err != nil {
		return nil, err
	}
	a.Edges.SupportPlan = supportPlan
	if _, err := s.generateAgreementCycles(ctx, a, now); err != nil {
		s.log.Warn("support agreement created but first cycle generation failed", zap.Error(err))
	}
	s.notifySupportStateChanged(ctx, tenantID, "support_agreement_created")
	return s.client.SupportAgreement.Get(ctx, a.ID)
}

// UpdateSupportAgreement applies a partial update. Cadence or timing changes re-anchor the
// schedule (see SupportAgreementInput.RescheduleFrom); an amount change reprices the agreement's
// not-yet-invoiced cycles; ending or pausing never touches already invoiced charges.
func (s *Service) UpdateSupportAgreement(ctx context.Context, id uuid.UUID, in SupportAgreementInput, actor uuid.UUID) (*ent.SupportAgreement, error) {
	a, err := s.client.SupportAgreement.Query().
		Where(supportagreement.IDEQ(id)).
		WithSupportPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, supportErr("support agreement not found")
		}
		return nil, err
	}
	now := time.Now().UTC()
	upd := &agreementSetters{}
	meta := cloneAny(a.Metadata)
	changes := []string{}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, supportErr("name must not be empty")
		}
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetName(name) })
		changes = append(changes, "name")
	}
	if in.Notes != nil {
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetNotes(*in.Notes) })
	}
	if in.Currency != nil && strings.TrimSpace(*in.Currency) != "" {
		upd.add(func(u *ent.SupportAgreementUpdateOne) {
			u.SetCurrency(strings.ToUpper(strings.TrimSpace(*in.Currency)))
		})
		changes = append(changes, "currency")
	}
	if in.BillingEmail != nil {
		if v := strings.TrimSpace(*in.BillingEmail); v != "" {
			meta["billing_email"] = v
		} else {
			delete(meta, "billing_email")
		}
	}
	if in.Collection != nil {
		personal, err := parseCollection(*in.Collection)
		if err != nil {
			return nil, err
		}
		// Applies to charges invoiced from now on; an invoice already raised keeps how it was
		// booked.
		if personal != IsPersonalAgreement(meta) {
			changes = append(changes, "collection")
		}
		if personal {
			meta[MetaCollection] = CollectionPersonal
		} else {
			delete(meta, MetaCollection)
		}
	}

	// Amount.
	repriced := false
	switch {
	case in.ClearAmount && in.Amount != nil:
		return nil, supportErr("amount and clear_amount are mutually exclusive")
	case in.ClearAmount:
		if a.Kind == supportagreement.KindSPECIAL {
			return nil, supportErr("a special support agreement needs an amount")
		}
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.ClearAmount() })
		a.Amount = nil
		repriced = true
	case in.Amount != nil:
		if *in.Amount < 0 || (a.Kind == supportagreement.KindSPECIAL && *in.Amount == 0) {
			return nil, supportErr("amount must be positive")
		}
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetAmount(*in.Amount) })
		v := *in.Amount
		a.Amount = &v
		repriced = true
	}
	if repriced {
		changes = append(changes, "amount")
	}

	// Cadence and timing.
	cycle, count, unit := a.BillingCycle, a.IntervalCount, string(a.IntervalUnit)
	if in.BillingCycle != nil {
		if cycle, err = NormalizeSupportCycle(*in.BillingCycle); err != nil || cycle == "" {
			return nil, supportErr("billing_cycle is invalid")
		}
	}
	if in.IntervalCount != nil {
		count = *in.IntervalCount
	}
	if in.IntervalUnit != nil {
		unit = *in.IntervalUnit
	}
	iv, err := ResolveSupportInterval(cycle, count, unit)
	if err != nil {
		return nil, supportErr("%s", err.Error())
	}
	timing := a.BillingTiming
	if in.BillingTiming != nil {
		timing = supportagreement.BillingTiming(strings.ToUpper(strings.TrimSpace(*in.BillingTiming)))
		if err := supportagreement.BillingTimingValidator(timing); err != nil {
			return nil, supportErr("billing_timing must be ADVANCE or ARREARS")
		}
	}
	cur := AgreementInterval(a)
	rescheduled := cycle != a.BillingCycle || iv != cur || timing != a.BillingTiming
	rescheduleNow := strings.EqualFold(strings.TrimSpace(in.RescheduleFrom), "now")
	waiveFrom := time.Time{} // waive not-yet-invoiced cycles whose period ends after this instant
	if rescheduled {
		upd.add(func(u *ent.SupportAgreementUpdateOne) {
			u.SetBillingCycle(cycle).SetIntervalCount(iv.Count).SetIntervalUnit(iv.Unit).SetBillingTiming(timing)
		})
		anchor := a.NextPeriodStart.UTC()
		if rescheduleNow {
			anchor = now
			waiveFrom = now
		}
		meta = withAnchor(meta, anchor, a.CycleCount)
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetNextPeriodStart(anchor) })
		changes = append(changes, "schedule")
	}

	// End date.
	switch {
	case in.ClearEndsAt:
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.ClearEndsAt() })
	case in.EndsAt != nil:
		if !in.EndsAt.After(a.StartsAt) {
			return nil, supportErr("ends_at must be after starts_at")
		}
		upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetEndsAt(in.EndsAt.UTC()) })
		changes = append(changes, "ends_at")
	}

	// Status.
	if in.Status != nil {
		st := supportagreement.Status(strings.ToUpper(strings.TrimSpace(*in.Status)))
		if err := supportagreement.StatusValidator(st); err != nil {
			return nil, supportErr("status must be ACTIVE, PAUSED or ENDED")
		}
		if st != a.Status {
			upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetStatus(st) })
			changes = append(changes, "status")
			switch st {
			case supportagreement.StatusACTIVE:
				// Paused periods are not billed: resume the schedule from today.
				if a.NextPeriodStart.Before(now) {
					meta = withAnchor(meta, now, a.CycleCount)
					upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetNextPeriodStart(now) })
				}
			case supportagreement.StatusENDED:
				if waiveFrom.IsZero() || now.Before(waiveFrom) {
					waiveFrom = now
				}
			}
		}
	}

	meta["last_changed_at"] = now.Format(time.RFC3339)
	if actor != uuid.Nil {
		meta["last_changed_by"] = actor.String()
	}
	if len(changes) > 0 {
		meta["last_change"] = strings.Join(changes, ",")
	}
	upd.add(func(u *ent.SupportAgreementUpdateOne) { u.SetMetadata(meta) })

	err = s.withTx(ctx, func(tx *ent.Tx) error {
		u := tx.SupportAgreement.UpdateOneID(id)
		for _, set := range upd.fns {
			set(u)
		}
		if err := u.Exec(ctx); err != nil {
			return err
		}
		pending := tx.SupportFeeCycle.Query().Where(
			supportfeecycle.AgreementIDEQ(id),
			supportfeecycle.StatusEQ(supportfeecycle.StatusPENDING),
		)
		if !waiveFrom.IsZero() {
			cancel, err := pending.Clone().Where(supportfeecycle.Or(
				supportfeecycle.PeriodEndGT(waiveFrom),
				supportfeecycle.PeriodEndIsNil(),
			)).All(ctx)
			if err != nil {
				return err
			}
			for _, c := range cancel {
				m := cloneAny(c.Metadata)
				m["waive_reason"] = "agreement rescheduled or ended"
				m["waived_at"] = now.Format(time.RFC3339)
				if err := tx.SupportFeeCycle.UpdateOneID(c.ID).
					SetStatus(supportfeecycle.StatusWAIVED).SetMetadata(m).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if repriced {
			amount := AgreementPeriodAmount(a, a.Edges.SupportPlan)
			if _, err := tx.SupportFeeCycle.Update().Where(
				supportfeecycle.AgreementIDEQ(id),
				supportfeecycle.StatusEQ(supportfeecycle.StatusPENDING),
				supportfeecycle.CustomPriceIsNil(),
			).SetBasePrice(amount).Save(ctx); err != nil {
				return err
			}
		}
		s.emitTenantSubscriptionUpdatedTx(ctx, tx, a.TenantID, "support_agreement_updated")
		return nil
	})
	if err != nil {
		return nil, err
	}

	fresh, err := s.client.SupportAgreement.Query().Where(supportagreement.IDEQ(id)).WithSupportPlan().Only(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.Status == supportagreement.StatusACTIVE {
		if _, err := s.generateAgreementCycles(ctx, fresh, now); err != nil {
			s.log.Warn("support agreement updated but cycle generation failed", zap.Error(err))
		}
	}
	return s.client.SupportAgreement.Get(ctx, id)
}

// ── Settlement ──────────────────────────────────────────────────────────────

// MarkSupportCyclePaid settles the cycle a payment was made for. referenceID is either the cycle
// UUID (treasury's invoice reference, used by manual and public-page payments) or the payref
// "SUPFEE-{slug}-{first 12 hex of the cycle id}" carried by payment intents. When neither
// resolves, the tenant's oldest invoiced or overdue cycle is settled. Idempotent.
func (s *Service) MarkSupportCyclePaid(ctx context.Context, tenantID uuid.UUID, referenceID, paymentRef string) (*ent.SupportFeeCycle, error) {
	cycle, err := s.resolvePaidSupportCycle(ctx, tenantID, referenceID)
	if err != nil || cycle == nil {
		return nil, err
	}
	if cycle.Status == supportfeecycle.StatusPAID {
		return cycle, nil
	}
	return s.settleSupportCycle(ctx, cycle, supportfeecycle.StatusPAID, map[string]any{
		"paid_via":    "payment",
		"payment_ref": paymentRef,
	})
}

// SettleSupportCycleManually records an offline payment or a waiver by the platform owner.
func (s *Service) SettleSupportCycleManually(ctx context.Context, cycleID uuid.UUID, status supportfeecycle.Status, reason string, actor uuid.UUID) (*ent.SupportFeeCycle, error) {
	if status != supportfeecycle.StatusPAID && status != supportfeecycle.StatusWAIVED {
		return nil, supportErr("status must be PAID or WAIVED")
	}
	cycle, err := s.client.SupportFeeCycle.Get(ctx, cycleID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, supportErr("support fee cycle not found")
		}
		return nil, err
	}
	if cycle.Status == supportfeecycle.StatusPAID || cycle.Status == supportfeecycle.StatusWAIVED {
		return nil, supportErr("cycle is already %s", strings.ToLower(string(cycle.Status)))
	}
	audit := map[string]any{"settled_manually_at": time.Now().UTC().Format(time.RFC3339)}
	if reason != "" {
		audit["settle_reason"] = reason
	}
	if actor != uuid.Nil {
		audit["settled_by"] = actor.String()
	}
	if status == supportfeecycle.StatusPAID {
		audit["paid_via"] = "manual"
	}
	return s.settleSupportCycle(ctx, cycle, status, audit)
}

func (s *Service) settleSupportCycle(ctx context.Context, cycle *ent.SupportFeeCycle, status supportfeecycle.Status, audit map[string]any) (*ent.SupportFeeCycle, error) {
	meta := cloneAny(cycle.Metadata)
	for k, v := range audit {
		meta[k] = v
	}
	var out *ent.SupportFeeCycle
	err := s.withTx(ctx, func(tx *ent.Tx) error {
		upd := tx.SupportFeeCycle.UpdateOneID(cycle.ID).
			SetStatus(status).
			ClearGraceUntil().
			SetMetadata(meta)
		if status == supportfeecycle.StatusPAID {
			upd = upd.SetPaidAt(time.Now().UTC())
		}
		var err error
		if out, err = upd.Save(ctx); err != nil {
			return err
		}
		// auth-api re-reads the support-fee claim on this event, so the next token refresh
		// lifts the mutation block without waiting for its cache to expire.
		s.emitTenantSubscriptionUpdatedTx(ctx, tx, cycle.TenantID, "support_fee_settled")
		return nil
	})
	return out, err
}

func (s *Service) resolvePaidSupportCycle(ctx context.Context, tenantID uuid.UUID, referenceID string) (*ent.SupportFeeCycle, error) {
	ref := strings.TrimSpace(referenceID)
	byTenant := s.client.SupportFeeCycle.Query().Where(supportfeecycle.TenantIDEQ(tenantID))

	if id, err := uuid.Parse(ref); err == nil {
		c, err := byTenant.Clone().Where(supportfeecycle.IDEQ(id)).Only(ctx)
		if err == nil || !ent.IsNotFound(err) {
			return c, err
		}
	} else if prefix, ok := payrefIDPrefix(ref); ok {
		// A recognised payref always resolves to its own cycle, whatever its status, so a
		// redelivered payment is a no-op and can never settle a different charge.
		c, err := byTenant.Clone().Where(func(sel *entsql.Selector) {
			sel.Where(entsql.P(func(b *entsql.Builder) {
				b.WriteString("CAST(").WriteString(sel.C(supportfeecycle.FieldID)).WriteString(" AS TEXT) LIKE ").Arg(prefix + "%")
			}))
		}).First(ctx)
		if err == nil || !ent.IsNotFound(err) {
			return c, err
		}
	}

	// Unrecognised reference: settle the oldest invoiced or overdue charge.
	c, err := byTenant.Clone().
		Where(supportfeecycle.StatusIn(supportfeecycle.StatusINVOICED, supportfeecycle.StatusOVERDUE)).
		Order(ent.Asc(supportfeecycle.FieldDueDate)).
		First(ctx)
	if ent.IsNotFound(err) {
		s.log.Warn("support fee payment received but no payable cycle matched",
			zap.String("tenant_id", tenantID.String()), zap.String("reference_id", referenceID))
		return nil, nil
	}
	return c, err
}

// payrefIDPrefix turns the entity segment of a "SUPFEE-{slug}-{12 hex}" payref into the matching
// UUID text prefix ("xxxxxxxx-xxxx").
func payrefIDPrefix(ref string) (string, bool) {
	i := strings.LastIndex(ref, "-")
	if i < 0 || len(ref)-i-1 != 12 {
		return "", false
	}
	seg := strings.ToLower(ref[i+1:])
	for _, r := range seg {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", false
		}
	}
	return seg[:8] + "-" + seg[8:], true
}

// ── Claim resolution ────────────────────────────────────────────────────────

// currentSupportFeeStatus feeds the support_fee_status / support_fee_due_at JWT claims. The due
// date is the OLDEST unpaid cycle across every agreement of the tenant, so one overdue charge
// keeps blocking even after newer periods were generated. Returns ("", nil, nil) when nothing is
// owed, which RequireSupportFeeCurrentForMutations treats as "always pass". Served by the
// (tenant_id, status, due_date) index.
func (s *Service) currentSupportFeeStatus(ctx context.Context, tenantID uuid.UUID) (string, *time.Time, error) {
	cycle, err := s.client.SupportFeeCycle.Query().
		Where(supportfeecycle.TenantIDEQ(tenantID), supportfeecycle.StatusIn(payableSupportStatuses...)).
		Order(ent.Asc(supportfeecycle.FieldDueDate)).
		Select(supportfeecycle.FieldDueDate, supportfeecycle.FieldStatus).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", nil, nil
		}
		return "", nil, err
	}
	due := cycle.DueDate.UTC()
	status := "CURRENT"
	if cycle.Status == supportfeecycle.StatusOVERDUE || time.Now().After(due) {
		status = "OVERDUE"
	}
	return status, &due, nil
}

// notifySupportStateChanged emits tenant.subscription.updated outside a caller transaction.
func (s *Service) notifySupportStateChanged(ctx context.Context, tenantID uuid.UUID, direction string) {
	if err := s.withTx(ctx, func(tx *ent.Tx) error {
		s.emitTenantSubscriptionUpdatedTx(ctx, tx, tenantID, direction)
		return nil
	}); err != nil {
		s.log.Warn("support state event failed", zap.Error(err))
	}
}

// emitTenantSubscriptionUpdatedTx writes the tenant.subscription.updated event auth-api listens
// to for refreshing its cached subscription (and so the support-fee claims).
func (s *Service) emitTenantSubscriptionUpdatedTx(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, direction string) {
	slug := ""
	if t, err := tx.Tenant.Get(ctx, tenantID); err == nil {
		slug = t.Slug
	}
	s.writeOutboxEvent(ctx, tx, tenantID, "tenant", tenantID, "subscription.updated", map[string]any{
		"tenant_id":   tenantID.String(),
		"tenant_slug": slug,
		"direction":   direction,
	})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *Service) withTx(ctx context.Context, fn func(tx *ent.Tx) error) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func cloneAny(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func currencyOr(c string) string {
	if c == "" {
		return "KES"
	}
	return c
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
