package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	events "github.com/Bengo-Hub/shared-events"
	"github.com/Bengo-Hub/shared-events/balancealert"
)

// Low-balance alerts for every metered service in credit_services.go.
//
// A spend through Consume that takes a tenant's remaining units across the low mark or to zero
// raises the central billing.balance.low event (shared-events balancealert), which
// notifications-api turns into an email and a WhatsApp message with a Buy credits button. The
// check, the event and the dedupe key are the shared helper's; this file only supplies the
// numbers the ledger knows:
//
//   - remaining is quota plus bundle for a service with a recurring allowance, the bundle
//     otherwise (prepaid-only services such as eTIMS API tokens);
//   - the low mark is billing.low_balance_threshold (default 20%) of the period's total, which
//     is the allowance plus the bundle when the allowance was reset; a service without an
//     allowance uses the bundle wallet's own low_balance_threshold;
//   - "once" is per tenant, service, level and month, re-armed by any top-up (the bundle
//     wallet's lifetime_granted is the epoch).
//
// The claim and the event are written in the spend's own transaction: the claim is a marker
// on the spend's ledger rows and the event goes to the outbox, so a rolled-back spend raises
// nothing and a committed one raises it exactly once. A new metered service needs only its
// registry entry.

// ConfigLowBalanceThreshold is the platform ServiceConfig key for the low mark ratio.
const ConfigLowBalanceThreshold = "billing.low_balance_threshold"

// ledgerAlertKey is the ledger metadata key that records an alert claim.
const ledgerAlertKey = "balance_alert"

var (
	buyCreditsMu   sync.RWMutex
	buyCreditsBase = "https://pricing.codevertexafrica.com"
)

// SetBuyCreditsBase sets the subscriptions UI base the alert's Buy credits link points at.
func SetBuyCreditsBase(base string) {
	if base = strings.TrimRight(strings.TrimSpace(base), "/"); base != "" {
		buyCreditsMu.Lock()
		buyCreditsBase = base
		buyCreditsMu.Unlock()
	}
}

func buyCreditsURL() string {
	buyCreditsMu.RLock()
	defer buyCreditsMu.RUnlock()
	return buyCreditsBase + "/billing"
}

// lowRatio caches the configured ratio for a few minutes, so a spend never waits on it.
var lowRatio = struct {
	sync.Mutex
	v  float64
	at time.Time
}{}

const lowRatioTTL = 5 * time.Minute

func (s *TokenWalletService) lowBalanceRatio(ctx context.Context, tx *sql.Tx) float64 {
	lowRatio.Lock()
	defer lowRatio.Unlock()
	if !lowRatio.at.IsZero() && time.Since(lowRatio.at) < lowRatioTTL {
		return lowRatio.v
	}
	v := balancealert.DefaultLowRatio
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT config_value FROM service_configs WHERE config_key = $1 AND tenant_id IS NULL LIMIT 1`,
		ConfigLowBalanceThreshold).Scan(&raw)
	if err == nil {
		if f, perr := strconv.ParseFloat(strings.TrimSpace(raw), 64); perr == nil && f > 0 && f < 1 {
			v = f
		}
	}
	lowRatio.v, lowRatio.at = v, time.Now()
	return v
}

// periodTotal is the allowance plus the bundle recorded when the allowance was last reset
// (SetQuota), or 0 when the service has no allowance this period.
func periodTotal(ctx context.Context, tx *sql.Tx, quota *walletRow) (int64, error) {
	if quota == nil {
		return 0, nil
	}
	var (
		target int64
		meta   []byte
	)
	err := tx.QueryRowContext(ctx, `
		SELECT new_balance, COALESCE(metadata, '{}'::jsonb) FROM api_token_transactions
		WHERE wallet_id = $1 AND ref_type = 'quota_reset'
		ORDER BY created_at DESC LIMIT 1`, quota.id).Scan(&target, &meta)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read period total: %w", err)
	}
	var m struct {
		PeriodTotal int64 `json:"period_total"`
	}
	_ = json.Unmarshal(meta, &m)
	if m.PeriodTotal > 0 {
		return m.PeriodTotal, nil
	}
	return target, nil
}

// balanceAlertFor raises the alert for a spend from before to after remaining units, inside
// tx. It returns the claim key to stamp on the spend's ledger rows, or "" when nothing crossed
// or the alert was already raised this period.
func (s *TokenWalletService) balanceAlertFor(ctx context.Context, tx *sql.Tx, req ConsumeRequest, bundle, quota *walletRow, before, after int64) (string, error) {
	spec := req.Service
	mark := float64(bundle.lowThreshold)
	total, err := periodTotal(ctx, tx, quota)
	if err != nil {
		return "", err
	}
	if total > 0 {
		mark = balancealert.LowMark(float64(total), s.lowBalanceRatio(ctx, tx))
	}
	level, crossed := balancealert.Crossed(float64(before), float64(after), mark)
	if !crossed {
		return "", nil
	}
	remaining := after
	if remaining < 0 {
		remaining = 0
	}
	label := spec.Label
	if label == "" {
		label = spec.Code
	}
	a := balancealert.Alert{
		Scope: balancealert.ScopeTenant, TenantID: req.TenantID.String(), Resource: spec.Code, ResourceLabel: label,
		Level: level, Remaining: float64(remaining), Unit: spec.unit(), Threshold: mark,
		Period: time.Now().UTC().Format("2006-01"), BuyURL: buyCreditsURL(), OccurredAt: time.Now().UTC(),
		Epoch: strconv.FormatInt(bundle.lifetime, 10),
	}
	key := a.DedupeKey()
	claim := func(ctx context.Context, key string, _ time.Duration) bool {
		var n int
		err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM api_token_transactions
			WHERE tenant_id = $1 AND service_tag IN ($2, $3) AND created_at >= date_trunc('month', now())
			  AND metadata->>'`+ledgerAlertKey+`' = $4`,
			req.TenantID, spec.BundleTag, nonEmpty(spec.QuotaTag, spec.BundleTag), key).Scan(&n)
		return err == nil && n == 0
	}
	sent, err := balancealert.Notify(ctx, claim, 0, a, func(ctx context.Context, evt *events.Event) error {
		return insertOutboxEvent(ctx, tx, evt)
	})
	if err != nil || !sent {
		return "", err
	}
	return key, nil
}

// insertOutboxEvent writes the full shared-events envelope to the outbox inside tx; the
// poller publishes it to billing.balance.low.
func insertOutboxEvent(ctx context.Context, tx *sql.Tx, evt *events.Event) error {
	env, err := evt.ToJSON()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'PENDING', 0, now())`,
		evt.ID, evt.TenantID, evt.AggregateType, evt.AggregateID, evt.EventType, string(env)); err != nil {
		return fmt.Errorf("write balance alert outbox: %w", err)
	}
	return nil
}

// unit names what the service counts, for the alert text.
func (s MeteredService) unit() string {
	if s.Unit != "" {
		return s.Unit
	}
	return "units"
}
