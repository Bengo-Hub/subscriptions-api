package billing

import (
	"context"
	"database/sql"
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
	db  *sql.DB
}

// NewTokenWalletService creates a new TokenWalletService. db is the raw SQL handle used by the
// transactional ledger in credit_ledger.go (the same database the ent client uses).
func NewTokenWalletService(log *zap.Logger, orm *ent.Client, db *sql.DB) *TokenWalletService {
	return &TokenWalletService{
		log: log.Named("billing.token_wallet"),
		orm: orm,
		db:  db,
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

// EnsureWallet provisions a zero-balance wallet row for tenant+service if one doesn't already
// exist — the public entry point for the auth.app.promoted_to_production consumer (see
// consumers/app_promotion.go). Idempotent: a second call for an already-provisioned wallet is a
// harmless no-op.
func (s *TokenWalletService) EnsureWallet(ctx context.Context, tenantID uuid.UUID, serviceTag string) error {
	_, err := s.ensureWallet(ctx, tenantID, serviceTag)
	return err
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

// DeductInput describes one metered spend attempt on a prepaid-only service.
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
// balance is insufficient. It is a prepaid-only spend through the shared Consume core, so two
// concurrent deductions racing the same wallet can never both succeed past the balance.
func (s *TokenWalletService) DeductTokens(ctx context.Context, in DeductInput) (DeductResult, error) {
	if in.Tokens < 0 {
		return DeductResult{}, fmt.Errorf("tokens must be >= 0")
	}
	spec, ok := LookupMeteredService(in.ServiceTag)
	if !ok {
		spec = MeteredService{Code: in.ServiceTag, BundleTag: in.ServiceTag}
	}
	res, err := s.Consume(ctx, ConsumeRequest{
		TenantID: in.TenantID, Service: spec, Units: in.Tokens,
		EndpointPattern: in.EndpointPattern, RefID: in.RefID, RefType: in.RefType, Description: in.Description,
	})
	if err != nil {
		return DeductResult{}, err
	}
	if !res.Allowed {
		return DeductResult{Allowed: false, NewBalance: res.BundleBalance}, nil
	}
	return DeductResult{Allowed: true, NewBalance: res.BundleBalance, Charged: in.Tokens}, nil
}

// GrantTokens credits a plan's included allowance. ADDITIVE: grants accumulate and never expire,
// per the confirmed product decision for API tokens. key makes one grant apply once per period
// (use the subscription id plus billing period).
func (s *TokenWalletService) GrantTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, refID uuid.UUID, description string) (int64, error) {
	bal, _, err := s.Credit(ctx, CreditRequest{
		TenantID: tenantID, ServiceTag: serviceTag, Units: tokens, Action: ledgerGrant,
		RefID: refID, RefType: "renewal", Description: description,
	})
	if err == nil {
		s.log.Info("tokens granted", zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag), zap.Int64("tokens", tokens))
	}
	return bal, err
}

// TopUpTokens credits a self-serve purchase. refID is the settled PaymentIntent id and doubles
// as the idempotency key, so a redelivered payment event credits once.
func (s *TokenWalletService) TopUpTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, unitCostKes float64, refID uuid.UUID) (int64, error) {
	key := ""
	if refID != uuid.Nil {
		key = "topup:" + refID.String()
	}
	bal, _, err := s.Credit(ctx, CreditRequest{
		TenantID: tenantID, ServiceTag: serviceTag, Units: tokens, Action: ledgerTopup, Key: key,
		RefID: refID, RefType: "payment", Description: "Top-up", UnitCostKES: &unitCostKes,
	})
	if err == nil {
		s.log.Info("tokens topped up", zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag), zap.Int64("tokens", tokens))
	}
	return bal, err
}

// RefundTokens reverses a deduction (e.g. a downstream write failed after the token was already
// spent). refID should be the original deduction's ref_id where available.
func (s *TokenWalletService) RefundTokens(ctx context.Context, tenantID uuid.UUID, serviceTag string, tokens int64, refID uuid.UUID, description string) (int64, error) {
	bal, _, err := s.Credit(ctx, CreditRequest{
		TenantID: tenantID, ServiceTag: serviceTag, Units: tokens, Action: ledgerRefund,
		RefID: refID, RefType: "refund", Description: description,
	})
	return bal, err
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
