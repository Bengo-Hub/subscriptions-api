package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// This file is the one credits engine behind every prepaid service in credit_services.go. All
// movements run in one database transaction with the affected wallet rows locked (SELECT ... FOR
// UPDATE, always in service_tag order so concurrent calls cannot deadlock). Wallet rows for one
// tenant and service are therefore serialised, which makes spending and replay checks exact
// without any cache. Every movement can carry an idempotency key, enforced by the unique index
// (wallet_id, idempotency_key), so a redelivered event or a retried send applies at most once.

// Ledger actions written to api_token_transactions.action.
const (
	ledgerDeduction  = "deduction"
	ledgerTopup      = "topup"
	ledgerGrant      = "grant"
	ledgerRefund     = "refund"
	ledgerAdjustment = "adjustment"
	ledgerOverage    = "overage"
)

// Consume reason codes returned when a spend is refused.
const (
	ReasonInsufficient = "insufficient_credits"
	ReasonOverageCap   = "overage_cap"
)

// ConsumeRequest is one spend of Units from a credit service.
type ConsumeRequest struct {
	TenantID uuid.UUID
	Service  MeteredService
	Units    int64
	// Key makes the spend idempotent. Empty means no replay protection.
	Key             string
	EndpointPattern string
	RefID           uuid.UUID
	RefType         string
	Description     string
	// SubscriptionID owns the overage charge row. Required when AllowOverage is true.
	SubscriptionID uuid.UUID
	// AllowOverage lets the spend run past quota and bundle at the service's unit price. The
	// caller sets it only after confirming the tenant is entitled to the service.
	AllowOverage bool
	// OverageCapKES refuses a spend that would push pending overage above this amount. Zero or
	// negative means no cap.
	OverageCapKES float64
	// UnlimitedQuota records quota spending without reducing the quota wallet (an unlimited tier).
	UnlimitedQuota bool
}

// ConsumeResult reports how a spend was funded.
type ConsumeResult struct {
	Allowed       bool    `json:"allowed"`
	Reason        string  `json:"reason,omitempty"`
	FromQuota     int64   `json:"from_quota"`
	FromBundle    int64   `json:"from_bundle"`
	Overage       int64   `json:"overage_units"`
	OverageKES    float64 `json:"overage_kes"`
	BundleBalance int64   `json:"bundle_balance"`
	QuotaBalance  int64   `json:"quota_balance"`
	Replayed      bool    `json:"replayed,omitempty"`
}

type walletRow struct {
	id      uuid.UUID
	tag     string
	balance int64
}

func legKey(key, leg string) string { return key + "|" + leg }

// lockWallets creates any missing wallet rows for the tags and returns them locked, keyed by tag.
func (s *TokenWalletService) lockWallets(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, tags []string) (map[string]*walletRow, error) {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	out := make(map[string]*walletRow, len(sorted))
	for _, tag := range sorted {
		if tag == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_token_wallets (id, tenant_id, service_tag, balance, lifetime_granted, low_balance_threshold, created_at, updated_at)
			VALUES ($1, $2, $3, 0, 0, 50, now(), now())
			ON CONFLICT (tenant_id, service_tag) DO NOTHING`, uuid.New(), tenantID, tag); err != nil {
			return nil, fmt.Errorf("ensure wallet %s: %w", tag, err)
		}
		w := &walletRow{tag: tag}
		if err := tx.QueryRowContext(ctx, `
			SELECT id, balance FROM api_token_wallets
			WHERE tenant_id = $1 AND service_tag = $2 FOR UPDATE`, tenantID, tag).Scan(&w.id, &w.balance); err != nil {
			return nil, fmt.Errorf("lock wallet %s: %w", tag, err)
		}
		out[tag] = w
	}
	return out, nil
}

type ledgerRow struct {
	tenantID    uuid.UUID
	wallet      *walletRow
	action      string
	tokens      int64
	newBalance  int64
	endpoint    string
	unitCost    *float64
	refID       uuid.UUID
	refType     string
	description string
	key         string
	metadata    map[string]any
}

// insertLedger appends one row. It returns false when the idempotency key already exists.
func insertLedger(ctx context.Context, tx *sql.Tx, r ledgerRow) (bool, error) {
	var endpoint, refType, desc, key any
	if r.endpoint != "" {
		endpoint = r.endpoint
	}
	if r.refType != "" {
		refType = r.refType
	}
	if r.description != "" {
		desc = r.description
	}
	if r.key != "" {
		key = r.key
	}
	var refID any
	if r.refID != uuid.Nil {
		refID = r.refID
	}
	var unit any
	if r.unitCost != nil {
		unit = *r.unitCost
	}
	meta := "{}"
	if len(r.metadata) > 0 {
		b, err := json.Marshal(r.metadata)
		if err != nil {
			return false, fmt.Errorf("marshal ledger metadata: %w", err)
		}
		meta = string(b)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO api_token_transactions
			(id, tenant_id, wallet_id, service_tag, action, tokens, new_balance, endpoint_pattern, unit_cost_kes,
			 ref_id, ref_type, description, idempotency_key, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, now())
		ON CONFLICT (wallet_id, idempotency_key) DO NOTHING`,
		uuid.New(), r.tenantID, r.wallet.id, r.wallet.tag, r.action, r.tokens, r.newBalance, endpoint, unit,
		refID, refType, desc, key, meta)
	if err != nil {
		return false, fmt.Errorf("insert ledger row: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func applyBalance(ctx context.Context, tx *sql.Tx, w *walletRow, delta int64, lifetime bool) error {
	q := `UPDATE api_token_wallets SET balance = balance + $1, updated_at = now() WHERE id = $2 AND balance + $1 >= 0`
	if lifetime && delta > 0 {
		q = `UPDATE api_token_wallets SET balance = balance + $1, lifetime_granted = lifetime_granted + $1, updated_at = now() WHERE id = $2 AND balance + $1 >= 0`
	}
	res, err := tx.ExecContext(ctx, q, delta, w.id)
	if err != nil {
		return fmt.Errorf("update wallet %s: %w", w.tag, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("wallet %s balance would go negative", w.tag)
	}
	w.balance += delta
	return nil
}

func (s *TokenWalletService) requireDB() error {
	if s.db == nil {
		return errors.New("credit ledger database not configured")
	}
	return nil
}

// Consume spends units from the service in quota, bundle, overage order. A refused spend writes
// nothing. A replayed key returns the original funding split without spending again.
func (s *TokenWalletService) Consume(ctx context.Context, req ConsumeRequest) (ConsumeResult, error) {
	if err := s.requireDB(); err != nil {
		return ConsumeResult{}, err
	}
	if req.Units < 0 {
		return ConsumeResult{}, errors.New("units must be >= 0")
	}
	spec := req.Service
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsumeResult{}, fmt.Errorf("begin consume tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	wallets, err := s.lockWallets(ctx, tx, req.TenantID, []string{spec.BundleTag, spec.QuotaTag})
	if err != nil {
		return ConsumeResult{}, err
	}
	bundle := wallets[spec.BundleTag]
	quota := wallets[spec.QuotaTag]

	if req.Units == 0 {
		return snapshotResult(bundle, quota, true), nil
	}

	if req.Key != "" {
		if res, found, err := s.replayConsume(ctx, tx, req, bundle, quota); err != nil {
			return ConsumeResult{}, err
		} else if found {
			return res, nil
		}
	}

	remaining := req.Units
	var fromQuota, fromBundle int64
	if quota != nil {
		if req.UnlimitedQuota {
			fromQuota = remaining
		} else if quota.balance > 0 {
			fromQuota = min64(remaining, quota.balance)
		}
		remaining -= fromQuota
	}
	if remaining > 0 && bundle.balance > 0 {
		fromBundle = min64(remaining, bundle.balance)
		remaining -= fromBundle
	}
	overage := remaining

	var overageKES float64
	if overage > 0 {
		if !spec.AllowsOverage() || !req.AllowOverage {
			return ConsumeResult{Reason: ReasonInsufficient, BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota)}, nil
		}
		overageKES = float64(overage) * spec.UnitPriceKES
		if req.OverageCapKES > 0 {
			var pending float64
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(total_charge_kes), 0) FROM overage_charges
				WHERE tenant_id = $1 AND metric_type = $2 AND status = 'pending'`, req.TenantID, spec.OverageMetric).Scan(&pending); err != nil {
				return ConsumeResult{}, fmt.Errorf("read pending overage: %w", err)
			}
			if pending+overageKES > req.OverageCapKES {
				return ConsumeResult{Reason: ReasonOverageCap, BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota)}, nil
			}
		}
		if req.SubscriptionID == uuid.Nil {
			return ConsumeResult{}, errors.New("subscription id is required to record overage")
		}
	}

	row := func(w *walletRow, action string, tokens int64, leg string, meta map[string]any) error {
		ok, err := insertLedger(ctx, tx, ledgerRow{
			tenantID: req.TenantID, wallet: w, action: action, tokens: tokens, newBalance: w.balance,
			endpoint: req.EndpointPattern, refID: req.RefID, refType: req.RefType, description: req.Description,
			key: legKeyIf(req.Key, leg), metadata: meta,
		})
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("consume key %q already used on wallet %s", req.Key, w.tag)
		}
		return nil
	}

	if fromQuota > 0 {
		if !req.UnlimitedQuota {
			if err := applyBalance(ctx, tx, quota, -fromQuota, false); err != nil {
				return ConsumeResult{}, err
			}
		}
		var quotaMeta map[string]any
		if req.UnlimitedQuota {
			quotaMeta = map[string]any{"unlimited": true}
		}
		if err := row(quota, ledgerDeduction, -fromQuota, "quota", quotaMeta); err != nil {
			return ConsumeResult{}, err
		}
	}
	if fromBundle > 0 {
		if err := applyBalance(ctx, tx, bundle, -fromBundle, false); err != nil {
			return ConsumeResult{}, err
		}
		if err := row(bundle, ledgerDeduction, -fromBundle, "bundle", nil); err != nil {
			return ConsumeResult{}, err
		}
	}
	if overage > 0 {
		if err := row(bundle, ledgerOverage, -overage, "overage", map[string]any{"kes": overageKES, "unit_price_kes": spec.UnitPriceKES}); err != nil {
			return ConsumeResult{}, err
		}
		if err := s.addOverage(ctx, tx, req, overage, overageKES); err != nil {
			return ConsumeResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return ConsumeResult{}, fmt.Errorf("commit consume tx: %w", err)
	}
	return ConsumeResult{
		Allowed: true, FromQuota: fromQuota, FromBundle: fromBundle, Overage: overage, OverageKES: overageKES,
		BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota),
	}, nil
}

func legKeyIf(key, leg string) string {
	if key == "" {
		return ""
	}
	return legKey(key, leg)
}

func balanceOf(w *walletRow) int64 {
	if w == nil {
		return 0
	}
	return w.balance
}

func snapshotResult(bundle, quota *walletRow, allowed bool) ConsumeResult {
	return ConsumeResult{Allowed: allowed, BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota)}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// replayConsume reconstructs the funding split of an earlier spend with the same key.
func (s *TokenWalletService) replayConsume(ctx context.Context, tx *sql.Tx, req ConsumeRequest, bundle, quota *walletRow) (ConsumeResult, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT idempotency_key, tokens, metadata FROM api_token_transactions
		WHERE tenant_id = $1 AND service_tag IN ($2, $3)
		  AND idempotency_key IN ($4, $5, $6)`,
		req.TenantID, req.Service.BundleTag, nonEmpty(req.Service.QuotaTag, req.Service.BundleTag),
		legKey(req.Key, "quota"), legKey(req.Key, "bundle"), legKey(req.Key, "overage"))
	if err != nil {
		return ConsumeResult{}, false, fmt.Errorf("replay lookup: %w", err)
	}
	defer rows.Close()
	res := ConsumeResult{Allowed: true, Replayed: true, BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota)}
	found := false
	for rows.Next() {
		var key string
		var tokens int64
		var meta []byte
		if err := rows.Scan(&key, &tokens, &meta); err != nil {
			return ConsumeResult{}, false, err
		}
		found = true
		switch key {
		case legKey(req.Key, "quota"):
			res.FromQuota = -tokens
		case legKey(req.Key, "bundle"):
			res.FromBundle = -tokens
		case legKey(req.Key, "overage"):
			res.Overage = -tokens
			var m map[string]any
			if json.Unmarshal(meta, &m) == nil {
				if v, ok := m["kes"].(float64); ok {
					res.OverageKES = v
				}
			}
		}
	}
	return res, found, rows.Err()
}

func nonEmpty(a, fallback string) string {
	if a != "" {
		return a
	}
	return fallback
}

// addOverage adds units to the tenant's open (pending) overage row for the service, creating it
// when none is open. One row accumulates across the cycle until an invoice marks it invoiced.
func (s *TokenWalletService) addOverage(ctx context.Context, tx *sql.Tx, req ConsumeRequest, units int64, kes float64) error {
	metric := req.Service.OverageMetric
	var id uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM overage_charges
		WHERE tenant_subscription_id = $1 AND metric_type = $2 AND status = 'pending'
		ORDER BY period_date LIMIT 1 FOR UPDATE`, req.SubscriptionID, metric).Scan(&id)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE overage_charges
			SET units_used = units_used + $1, units_over = units_over + $1, total_charge_kes = total_charge_kes + $2
			WHERE id = $3`, float64(units), kes, id); err != nil {
			return fmt.Errorf("update overage charge: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO overage_charges
				(id, tenant_subscription_id, tenant_id, metric_type, period_date, units_used, plan_limit, units_over,
				 unit_price_kes, total_charge_kes, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, 0, $6, $7, $8, 'pending', now())`,
			uuid.New(), req.SubscriptionID, req.TenantID, metric, time.Now().UTC(), float64(units),
			req.Service.UnitPriceKES, kes); err != nil {
			return fmt.Errorf("insert overage charge: %w", err)
		}
	default:
		return fmt.Errorf("find open overage charge: %w", err)
	}
	return nil
}

// RefundConsume reverses a spend identified by its consume key, for example when the provider
// rejected the message. It is idempotent on refundKey. Overage already put on an invoice is not
// reversed here; the refund only restores units that are still spendable.
func (s *TokenWalletService) RefundConsume(ctx context.Context, tenantID uuid.UUID, spec MeteredService, subscriptionID uuid.UUID, consumeKey, refundKey string) (ConsumeResult, error) {
	if err := s.requireDB(); err != nil {
		return ConsumeResult{}, err
	}
	if consumeKey == "" || refundKey == "" {
		return ConsumeResult{}, errors.New("consume and refund keys are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConsumeResult{}, fmt.Errorf("begin refund tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	wallets, err := s.lockWallets(ctx, tx, tenantID, []string{spec.BundleTag, spec.QuotaTag})
	if err != nil {
		return ConsumeResult{}, err
	}
	bundle := wallets[spec.BundleTag]
	quota := wallets[spec.QuotaTag]

	var already int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM api_token_transactions
		WHERE tenant_id = $1 AND service_tag IN ($2, $3) AND idempotency_key IN ($4, $5, $6)`,
		tenantID, spec.BundleTag, nonEmpty(spec.QuotaTag, spec.BundleTag),
		legKey(refundKey, "quota"), legKey(refundKey, "bundle"), legKey(refundKey, "overage")).Scan(&already); err != nil {
		return ConsumeResult{}, fmt.Errorf("refund replay lookup: %w", err)
	}
	if already > 0 {
		r := snapshotResult(bundle, quota, true)
		r.Replayed = true
		return r, nil
	}

	orig, found, err := s.replayConsume(ctx, tx, ConsumeRequest{TenantID: tenantID, Service: spec, Key: consumeKey}, bundle, quota)
	if err != nil {
		return ConsumeResult{}, err
	}
	if !found {
		return snapshotResult(bundle, quota, false), nil
	}

	put := func(w *walletRow, units int64, leg string, meta map[string]any) error {
		ok, err := insertLedger(ctx, tx, ledgerRow{
			tenantID: tenantID, wallet: w, action: ledgerRefund, tokens: units, newBalance: w.balance,
			refType: "refund", description: "Refund of " + consumeKey, key: legKey(refundKey, leg), metadata: meta,
		})
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("refund key %q already used", refundKey)
		}
		return nil
	}
	if orig.FromQuota > 0 && quota != nil {
		// An unlimited quota never reduced the wallet, so only restore a finite one.
		var unlimited bool
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE((metadata->>'unlimited')::boolean, false) FROM api_token_transactions
			WHERE wallet_id = $1 AND idempotency_key = $2`, quota.id, legKey(consumeKey, "quota")).Scan(&unlimited); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ConsumeResult{}, err
		}
		if !unlimited {
			if err := applyBalance(ctx, tx, quota, orig.FromQuota, false); err != nil {
				return ConsumeResult{}, err
			}
		}
		if err := put(quota, orig.FromQuota, "quota", nil); err != nil {
			return ConsumeResult{}, err
		}
	}
	if orig.FromBundle > 0 {
		if err := applyBalance(ctx, tx, bundle, orig.FromBundle, false); err != nil {
			return ConsumeResult{}, err
		}
		if err := put(bundle, orig.FromBundle, "bundle", nil); err != nil {
			return ConsumeResult{}, err
		}
	}
	if orig.Overage > 0 {
		reversed := false
		if subscriptionID != uuid.Nil {
			res, err := tx.ExecContext(ctx, `
				UPDATE overage_charges
				SET units_used = units_used - $1, units_over = units_over - $1, total_charge_kes = total_charge_kes - $2
				WHERE id = (SELECT id FROM overage_charges
				            WHERE tenant_subscription_id = $3 AND metric_type = $4 AND status = 'pending'
				              AND units_over >= $1 AND total_charge_kes >= $2
				            ORDER BY period_date LIMIT 1)`,
				float64(orig.Overage), orig.OverageKES, subscriptionID, spec.OverageMetric)
			if err != nil {
				return ConsumeResult{}, fmt.Errorf("reverse overage: %w", err)
			}
			n, _ := res.RowsAffected()
			reversed = n == 1
		}
		if err := put(bundle, orig.Overage, "overage", map[string]any{"kes": orig.OverageKES, "reversed": reversed}); err != nil {
			return ConsumeResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ConsumeResult{}, fmt.Errorf("commit refund tx: %w", err)
	}
	return ConsumeResult{Allowed: true, FromQuota: orig.FromQuota, FromBundle: orig.FromBundle, Overage: orig.Overage,
		OverageKES: orig.OverageKES, BundleBalance: bundle.balance, QuotaBalance: balanceOf(quota)}, nil
}

// CreditRequest adds units to one wallet.
type CreditRequest struct {
	TenantID    uuid.UUID
	ServiceTag  string
	Units       int64
	Action      string // topup, grant, refund or adjustment
	Key         string
	RefID       uuid.UUID
	RefType     string
	Description string
	UnitCostKES *float64
}

// Credit adds units. With a key it applies at most once; applied reports whether this call wrote.
func (s *TokenWalletService) Credit(ctx context.Context, req CreditRequest) (newBalance int64, applied bool, err error) {
	if err := s.requireDB(); err != nil {
		return 0, false, err
	}
	if req.Units <= 0 {
		return 0, false, errors.New("units must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin credit tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	wallets, err := s.lockWallets(ctx, tx, req.TenantID, []string{req.ServiceTag})
	if err != nil {
		return 0, false, err
	}
	w := wallets[req.ServiceTag]

	if req.Key != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM api_token_transactions WHERE wallet_id = $1 AND idempotency_key = $2`, w.id, req.Key).Scan(&n); err != nil {
			return 0, false, fmt.Errorf("credit replay lookup: %w", err)
		}
		if n > 0 {
			return w.balance, false, nil
		}
	}
	lifetime := req.Action == ledgerTopup || req.Action == ledgerGrant
	if err := applyBalance(ctx, tx, w, req.Units, lifetime); err != nil {
		return 0, false, err
	}
	if _, err := insertLedger(ctx, tx, ledgerRow{
		tenantID: req.TenantID, wallet: w, action: req.Action, tokens: req.Units, newBalance: w.balance,
		unitCost: req.UnitCostKES, refID: req.RefID, refType: req.RefType, description: req.Description, key: req.Key,
	}); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit credit tx: %w", err)
	}
	return w.balance, true, nil
}

// SetQuota resets a recurring allowance wallet to target units with one adjustment row. It is
// idempotent per key (use the period window start), so a repeated job run changes nothing.
func (s *TokenWalletService) SetQuota(ctx context.Context, tenantID uuid.UUID, quotaTag string, target int64, key, description string) (applied bool, err error) {
	if err := s.requireDB(); err != nil {
		return false, err
	}
	if target < 0 {
		target = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin quota tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	wallets, err := s.lockWallets(ctx, tx, tenantID, []string{quotaTag})
	if err != nil {
		return false, err
	}
	w := wallets[quotaTag]
	if key != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM api_token_transactions WHERE wallet_id = $1 AND idempotency_key = $2`, w.id, key).Scan(&n); err != nil {
			return false, fmt.Errorf("quota replay lookup: %w", err)
		}
		if n > 0 {
			return false, nil
		}
	}
	delta := target - w.balance
	if _, err := tx.ExecContext(ctx, `UPDATE api_token_wallets SET balance = $1, updated_at = now() WHERE id = $2`, target, w.id); err != nil {
		return false, fmt.Errorf("set quota: %w", err)
	}
	w.balance = target
	if _, err := insertLedger(ctx, tx, ledgerRow{
		tenantID: tenantID, wallet: w, action: ledgerAdjustment, tokens: delta, newBalance: target,
		refType: "quota_reset", description: description, key: key,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit quota tx: %w", err)
	}
	return true, nil
}
