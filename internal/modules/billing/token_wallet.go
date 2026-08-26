package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/apitokentransaction"
	"github.com/bengobox/subscription-service/internal/ent/apitokenwallet"
)

// TokenWalletService manages prepaid per-tenant, per-service API token wallets for metered
// external API products (e.g. the external eTIMS fiscalization API, service_tag "etims_api").
// Deliberately generalized by service_tag rather than eTIMS-specific — a future external API
// (notifications, SSO) reuses this same primitive instead of a copy-pasted wallet.
//
// Unlike SubscriptionCredit (which is applied once at renewal), this wallet is gated in REAL
// TIME: DeductTokens refuses the spend outright once the balance runs out, via a single
// conditional atomic UPDATE (never a read-then-write), so concurrent requests can't overspend
// a wallet — no explicit row lock needed, the WHERE clause + atomic SET IS the concurrency
// control at the database level.
type TokenWalletService struct {
	log *zap.Logger
	orm *ent.Client
}

// NewTokenWalletService creates a new TokenWalletService.
func NewTokenWalletService(log *zap.Logger, orm *ent.Client) *TokenWalletService {
	return &TokenWalletService{
		log: log.Named("billing.token_wallet"),
		orm: orm,
	}
}

// WalletSnapshot is the tenant-facing view of a wallet's current state.
type WalletSnapshot struct {
	Balance             int64 `json:"balance"`
	LifetimeGranted     int64 `json:"lifetime_granted"`
	LowBalanceThreshold int64 `json:"low_balance_threshold"`
	LowBalance          bool  `json:"low_balance"`
}

// GetBalance returns the current wallet snapshot for a tenant+service. Returns a zero-value
// snapshot (not an error) if no wallet exists yet — a wallet is created lazily on first grant,
// top-up, or deduction attempt.
func (s *TokenWalletService) GetBalance(ctx context.Context, tenantID uuid.UUID, serviceTag string) (WalletSnapshot, error) {
	w, err := s.orm.ApiTokenWallet.Query().
		Where(apitokenwallet.TenantIDEQ(tenantID), apitokenwallet.ServiceTagEQ(serviceTag)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return WalletSnapshot{}, nil
		}
		return WalletSnapshot{}, fmt.Errorf("get wallet: %w", err)
	}
	return WalletSnapshot{
		Balance:             w.Balance,
		LifetimeGranted:     w.LifetimeGranted,
		LowBalanceThreshold: w.LowBalanceThreshold,
		LowBalance:          w.Balance <= w.LowBalanceThreshold,
	}, nil
}

// ensureWallet returns the existing wallet or creates one at zero balance. Creating the row is
// deliberately NOT a token grant — a freshly-provisioned integrator (see the auth.app.promoted_to_
// production consumer) gets an empty wallet, not free tokens; they still have to subscribe to a
// plan or buy a top-up before their balance is non-zero.
func (s *TokenWalletService) ensureWallet(ctx context.Context, tenantID uuid.UUID, serviceTag string) (*ent.ApiTokenWallet, error) {
	w, err := s.orm.ApiTokenWallet.Query().
		Where(apitokenwallet.TenantIDEQ(tenantID), apitokenwallet.ServiceTagEQ(serviceTag)).
		Only(ctx)
	if err == nil {
		return w, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query wallet: %w", err)
	}
	w, err = s.orm.ApiTokenWallet.Create().
		SetTenantID(tenantID).
		SetServiceTag(serviceTag).
		SetBalance(0).
		SetLifetimeGranted(0).
		Save(ctx)
	if err != nil {
		// A concurrent request may have created the wallet between our lookup and this insert
		// (the unique index on tenant_id+service_tag is the real guard) — re-query rather than
		// treat that as a failure.
		if ent.IsConstraintError(err) {
			return s.orm.ApiTokenWallet.Query().
				Where(apitokenwallet.TenantIDEQ(tenantID), apitokenwallet.ServiceTagEQ(serviceTag)).
				Only(ctx)
		}
		return nil, fmt.Errorf("create wallet: %w", err)
	}
	s.log.Info("token wallet provisioned",
		zap.String("tenant_id", tenantID.String()),
		zap.String("service_tag", serviceTag),
	)
	return w, nil
}

// DeductInput describes one metered spend attempt.
type DeductInput struct {
	TenantID        uuid.UUID
	ServiceTag      string
	Tokens          int64
	EndpointPattern string
	RefID           uuid.UUID
	RefType         string
	Description     string
}

// DeductResult reports the outcome of a DeductTokens call.
type DeductResult struct {
	Allowed    bool  `json:"allowed"`
	NewBalance int64 `json:"new_balance"`
	Charged    int64 `json:"tokens_charged"`
}

// DeductTokens atomically spends tokens from a tenant's wallet, refusing the spend if the
// balance is insufficient. The allow/deny decision and the balance mutation happen in ONE
// conditional UPDATE (WHERE balance >= tokens), so two concurrent deductions racing the same
// wallet can never both succeed past the point where only one of them fits — the loser's
// affected-row-count is 0, not a stale read.
func (s *TokenWalletService) DeductTokens(ctx context.Context, in DeductInput) (DeductResult, error) {
	if in.Tokens < 0 {
		return DeductResult{}, fmt.Errorf("tokens must be >= 0")
	}
	wallet, err := s.ensureWallet(ctx, in.TenantID, in.ServiceTag)
	if err != nil {
		return DeductResult{}, err
	}
	if in.Tokens == 0 {
		// A zero-weight endpoint (e.g. a free lookup) — nothing to deduct, always allowed.
		return DeductResult{Allowed: true, NewBalance: wallet.Balance, Charged: 0}, nil
	}

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return DeductResult{}, fmt.Errorf("begin deduct tx: %w", err)
	}

	affected, err := tx.ApiTokenWallet.Update().
		Where(apitokenwallet.IDEQ(wallet.ID), apitokenwallet.BalanceGTE(in.Tokens)).
		AddBalance(-in.Tokens).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return DeductResult{}, fmt.Errorf("deduct balance: %w", err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		// Insufficient balance — report the CURRENT balance (may have changed since ensureWallet
		// read it) so the caller's 402 body is accurate.
		cur, cerr := s.orm.ApiTokenWallet.Get(ctx, wallet.ID)
		bal := wallet.Balance
		if cerr == nil {
			bal = cur.Balance
		}
		return DeductResult{Allowed: false, NewBalance: bal, Charged: 0}, nil
	}

	updated, err := tx.ApiTokenWallet.Get(ctx, wallet.ID)
	if err != nil {
		_ = tx.Rollback()
		return DeductResult{}, fmt.Errorf("reload wallet after deduct: %w", err)
	}

	txCreate := tx.ApiTokenTransaction.Create().
		SetTenantID(in.TenantID).
		SetWalletID(wallet.ID).
		SetServiceTag(in.ServiceTag).
		SetAction(apitokentransaction.ActionDeduction).
		SetTokens(-in.Tokens).
		SetNewBalance(updated.Balance)
	if in.EndpointPattern != "" {
		txCreate = txCreate.SetEndpointPattern(in.EndpointPattern)
	}
	if in.RefID != uuid.Nil {
		txCreate = txCreate.SetRefID(in.RefID)
	}
	if in.RefType != "" {
		txCreate = txCreate.SetRefType(in.RefType)
	}
	if in.Description != "" {
		txCreate = txCreate.SetDescription(in.Description)
	}
	if _, err := txCreate.Save(ctx); err != nil {
		_ = tx.Rollback()
		return DeductResult{}, fmt.Errorf("record deduction: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DeductResult{}, fmt.Errorf("commit deduct tx: %w", err)
	}

	return DeductResult{Allowed: true, NewBalance: updated.Balance, Charged: in.Tokens}, nil
}

// creditAction is one of grant|topup|refund|adjustment — the positive-movement actions, which
// unlike deduction can never be refused (a wallet balance can't overflow in a way that matters
// here), so they share one atomic-add implementation.
func (s *TokenWalletService) credit(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, action apitokentransaction.Action, unitCostKes *float64, refID uuid.UUID, refType, description string) (int64, error) {
	if tokens <= 0 {
		return 0, fmt.Errorf("tokens must be positive")
	}
	wallet, err := s.ensureWallet(ctx, tenantID, serviceTag)
	if err != nil {
		return 0, err
	}

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin credit tx: %w", err)
	}

	update := tx.ApiTokenWallet.UpdateOneID(wallet.ID).
		AddBalance(tokens).
		SetUpdatedAt(time.Now())
	if action == apitokentransaction.ActionGrant || action == apitokentransaction.ActionTopup {
		update = update.AddLifetimeGranted(tokens)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("credit balance: %w", err)
	}

	txCreate := tx.ApiTokenTransaction.Create().
		SetTenantID(tenantID).
		SetWalletID(wallet.ID).
		SetServiceTag(serviceTag).
		SetAction(action).
		SetTokens(tokens).
		SetNewBalance(updated.Balance)
	if unitCostKes != nil {
		txCreate = txCreate.SetUnitCostKes(*unitCostKes)
	}
	if refID != uuid.Nil {
		txCreate = txCreate.SetRefID(refID)
	}
	if refType != "" {
		txCreate = txCreate.SetRefType(refType)
	}
	if description != "" {
		txCreate = txCreate.SetDescription(description)
	}
	if _, err := txCreate.Save(ctx); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("record credit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit credit tx: %w", err)
	}
	return updated.Balance, nil
}

// GrantTokens credits a plan's included monthly token allowance. ADDITIVE — grants accumulate
// and never expire/reset, per the confirmed product decision (a light-usage month isn't
// punished; a purchased top-up and a monthly grant behave identically once in the wallet).
// refID should be the tenant_subscription id + billing period as a stable per-period
// idempotency key at the call site (mirrors how EarnLoyaltyCredits keys on payment ref) —
// callers are responsible for not double-granting the same period, this method itself does not
// dedupe.
func (s *TokenWalletService) GrantTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, refID uuid.UUID, description string) (int64, error) {
	bal, err := s.credit(ctx, tenantID, serviceTag, tokens, apitokentransaction.ActionGrant, nil, refID, "renewal", description)
	if err == nil {
		s.log.Info("tokens granted", zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag), zap.Int64("tokens", tokens))
	}
	return bal, err
}

// TopUpTokens credits a self-serve purchase. refID is the settled PaymentIntent id.
func (s *TokenWalletService) TopUpTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, unitCostKes float64, refID uuid.UUID) (int64, error) {
	bal, err := s.credit(ctx, tenantID, serviceTag, tokens, apitokentransaction.ActionTopup, &unitCostKes, refID, "payment", "Token top-up")
	if err == nil {
		s.log.Info("tokens topped up", zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag), zap.Int64("tokens", tokens))
	}
	return bal, err
}

// RefundTokens reverses a deduction (e.g. a downstream write failed after the token was already
// spent). refID should be the original deduction's ref_id where available.
func (s *TokenWalletService) RefundTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, refID uuid.UUID, description string) (int64, error) {
	return s.credit(ctx, tenantID, serviceTag, tokens, apitokentransaction.ActionRefund, nil, refID, "refund", description)
}

// TransactionEntry is a summarized ledger row for the API.
type TransactionEntry struct {
	ID              string    `json:"id"`
	Action          string    `json:"action"`
	Tokens          int64     `json:"tokens"`
	NewBalance      int64     `json:"new_balance"`
	EndpointPattern string    `json:"endpoint_pattern,omitempty"`
	UnitCostKes     *float64  `json:"unit_cost_kes,omitempty"`
	Description     string    `json:"description,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ListTransactions returns paginated ledger entries for a tenant+service, newest first.
func (s *TokenWalletService) ListTransactions(ctx context.Context, tenantID uuid.UUID, serviceTag string, limit, offset int) ([]TransactionEntry, int, error) {
	q := s.orm.ApiTokenTransaction.Query().
		Where(apitokentransaction.TenantIDEQ(tenantID), apitokentransaction.ServiceTagEQ(serviceTag)).
		Order(ent.Desc(apitokentransaction.FieldCreatedAt))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count transactions: %w", err)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Offset(offset).Limit(limit).All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	entries := make([]TransactionEntry, 0, len(rows))
	for _, r := range rows {
		entry := TransactionEntry{
			ID:          r.ID.String(),
			Action:      string(r.Action),
			Tokens:      r.Tokens,
			NewBalance:  r.NewBalance,
			UnitCostKes: r.UnitCostKes,
			CreatedAt:   r.CreatedAt,
		}
		if r.EndpointPattern != nil {
			entry.EndpointPattern = *r.EndpointPattern
		}
		if r.Description != nil {
			entry.Description = *r.Description
		}
		entries = append(entries, entry)
	}
	return entries, total, nil
}
