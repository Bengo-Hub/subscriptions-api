package billing

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredit"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredittransaction"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CreditService manages the per-tenant subscription credit wallet.
// Credits are earned as loyalty (% of subscription payments) or gifted by platform admins,
// and are auto-applied against the next renewal invoice.
type CreditService struct {
	log *zap.Logger
	orm *ent.Client
}

// NewCreditService creates a new CreditService.
func NewCreditService(log *zap.Logger, orm *ent.Client) *CreditService {
	return &CreditService{
		log: log.Named("billing.credit"),
		orm: orm,
	}
}

// GetBalance returns the current credit wallet balance for a tenant.
// Returns 0 if no wallet exists yet (not an error — wallet is created lazily).
func (s *CreditService) GetBalance(ctx context.Context, tenantID uuid.UUID) (int, error) {
	wallet, err := s.orm.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("get wallet: %w", err)
	}
	return wallet.BalanceKes, nil
}

// AddCredits adds credits to the tenant's wallet and records the transaction.
// txType must be one of: earned, coupon_redeemed, gifted, manual_adjusted.
// refID and refType identify the source entity (payment UUID, coupon UUID, etc.).
func (s *CreditService) AddCredits(ctx context.Context, tenantID uuid.UUID, amountKes int, txType string, refID uuid.UUID, refType string) error {
	if amountKes <= 0 {
		return fmt.Errorf("credit amount must be positive")
	}

	wallet, err := s.ensureWallet(ctx, tenantID)
	if err != nil {
		return err
	}

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin credit tx: %w", err)
	}
	if err := s.addCreditsTx(ctx, tx, wallet.ID, tenantID, amountKes, txType, refID, refType); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit credit tx: %w", err)
	}

	s.log.Info("credits added",
		zap.String("tenant_id", tenantID.String()),
		zap.Int("amount_kes", amountKes),
		zap.String("type", txType),
	)
	return nil
}

// addCreditsTx applies one credit movement (wallet balance + audit row) inside an existing
// transaction. The caller owns commit/rollback, which lets a caller bundle extra state into
// the same atomic unit (see EarnReferralBonus, which flips referral_bonus_paid here too).
//
// The balance is incremented ATOMICALLY in SQL — AddBalanceKes compiles to
// "SET balance_kes = balance_kes + $n" — never read-modify-written in Go. A read-then-write
// lost updates whenever two credit events for the same wallet overlapped (e.g. a loyalty
// earn and a referral bonus landing from two NATS deliveries at once): both read the same
// starting balance and the second write silently discarded the first credit.
func (s *CreditService) addCreditsTx(ctx context.Context, tx *ent.Tx, walletID, tenantID uuid.UUID, amountKes int, txType string, refID uuid.UUID, refType string) error {
	if _, err := tx.SubscriptionCredit.UpdateOneID(walletID).
		AddBalanceKes(amountKes).
		AddLifetimeEarnedKes(amountKes).
		SetUpdatedAt(time.Now()).
		Save(ctx); err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}

	if _, err := tx.SubscriptionCreditTransaction.Create().
		SetTenantID(tenantID).
		SetCreditID(walletID).
		SetType(subscriptioncredittransaction.Type(txType)).
		SetAmountKes(amountKes).
		SetNillableRefID(&refID).
		SetNillableRefType(&refType).
		Save(ctx); err != nil {
		return fmt.Errorf("record credit transaction: %w", err)
	}
	return nil
}

// ApplyCreditsToRenewal deducts available credits from the renewal invoice amount.
// Returns the net charge after credit deduction and records an auto_applied transaction.
// If balance is 0, returns invoiceAmount unchanged.
func (s *CreditService) ApplyCreditsToRenewal(ctx context.Context, tenantID uuid.UUID, invoiceAmountKes int) (netChargeKes int, creditsApplied int, err error) {
	balance, err := s.GetBalance(ctx, tenantID)
	if err != nil {
		return invoiceAmountKes, 0, err
	}
	if balance <= 0 {
		return invoiceAmountKes, 0, nil
	}

	apply := balance
	if apply > invoiceAmountKes {
		apply = invoiceAmountKes
	}

	wallet, err := s.orm.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return invoiceAmountKes, 0, fmt.Errorf("get wallet for apply: %w", err)
	}

	_, err = s.orm.SubscriptionCredit.UpdateOneID(wallet.ID).
		SetBalanceKes(wallet.BalanceKes - apply).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return invoiceAmountKes, 0, fmt.Errorf("deduct credits: %w", err)
	}

	refID := tenantID // use tenant_id as self-referential ref for auto_applied
	refType := "renewal"
	if _, err := s.orm.SubscriptionCreditTransaction.Create().
		SetTenantID(tenantID).
		SetCreditID(wallet.ID).
		SetType(subscriptioncredittransaction.TypeAutoApplied).
		SetAmountKes(-apply).
		SetNillableRefID(&refID).
		SetNillableRefType(&refType).
		Save(ctx); err != nil {
		s.log.Warn("failed to record auto_applied credit transaction", zap.Error(err))
	}

	s.log.Info("credits applied to renewal",
		zap.String("tenant_id", tenantID.String()),
		zap.Int("credits_applied", apply),
		zap.Int("net_charge", invoiceAmountKes-apply),
	)
	return invoiceAmountKes - apply, apply, nil
}

// EarnLoyaltyCredits credits the loyalty earn amount after a successful subscription payment.
// Called by the payment consumer after treasury confirms payment.
func (s *CreditService) EarnLoyaltyCredits(ctx context.Context, tenantID uuid.UUID, paymentAmountKes int, paymentRefID uuid.UUID) error {
	wallet, err := s.ensureWallet(ctx, tenantID)
	if err != nil {
		return err
	}

	earnKes := int(float64(paymentAmountKes) * wallet.LoyaltyRate)
	if earnKes <= 0 {
		return nil
	}

	// This path has no application-level dedup of its own: the partial unique index on
	// (tenant_id, type, ref_id) IS the guard, so a NATS redelivery of the same payment is a
	// constraint violation, not a failure. Swallow it as the no-op it is — surfacing it would
	// make every routine redelivery look like a broken payout.
	err = s.AddCredits(ctx, tenantID, earnKes, "earned", paymentRefID, "payment")
	if err != nil && ent.IsConstraintError(err) {
		s.log.Info("loyalty credits already earned for this payment, skipping",
			zap.String("tenant_id", tenantID.String()),
			zap.String("payment_ref_id", paymentRefID.String()),
		)
		return nil
	}
	return err
}

// ReferralBonusRateConfigKey is the platform-level service_configs key holding the Type-A
// referral bonus rate as a bare decimal fraction (config_type "float"), e.g. "0.10" = 10%.
// This is the ONLY source of truth for the Type-A rate in this service; set it via the
// platform admin service-configs API (POST /platform/service-configs) with tenant_id NULL.
//
// NOTE: this is unrelated to treasury-api's referral_programs.revenue_share_percentage,
// which governs the entirely separate Type-B external-referrer equity flow in that service.
const ReferralBonusRateConfigKey = "subscriptions.referral_bonus_rate"

// DefaultReferralBonusRate is used when ReferralBonusRateConfigKey is unset, unparseable, or
// out of the (0,1] range, so existing deployments keep the historical 10% behaviour.
const DefaultReferralBonusRate = 0.10

// referralBonusRate resolves the configured Type-A bonus rate, falling back to
// DefaultReferralBonusRate. Read per payout (payouts are rare) so a rate change takes effect
// without a redeploy.
func (s *CreditService) referralBonusRate(ctx context.Context) float64 {
	cfg, err := s.orm.ServiceConfig.Query().
		Where(
			serviceconfig.ConfigKeyEQ(ReferralBonusRateConfigKey),
			serviceconfig.TenantIDIsNil(),
		).
		Only(ctx)
	if err != nil {
		// Not configured (the common case) — silent default.
		return DefaultReferralBonusRate
	}
	rate, perr := strconv.ParseFloat(strings.TrimSpace(cfg.ConfigValue), 64)
	if perr != nil || rate <= 0 || rate > 1 {
		s.log.Warn("invalid referral bonus rate in service_configs, using default",
			zap.String("config_key", ReferralBonusRateConfigKey),
			zap.String("config_value", cfg.ConfigValue),
			zap.Float64("default", DefaultReferralBonusRate),
		)
		return DefaultReferralBonusRate
	}
	return rate
}

// EarnReferralBonus credits a Type-A referral bonus to the REFERRER's wallet the FIRST time a
// tenant they referred makes a successful subscription payment. The bonus rewards bringing in
// the referral, so it is paid ONCE per referred tenant — not as a perpetual revenue share on
// every renewal that tenant ever pays.
//
// Two independent guards, both evaluated inside the payout transaction:
//   - tenant_subscriptions.referral_bonus_paid on the REFERRED tenant: the lifetime one-time
//     gate. Flipped in the same transaction as the credit, so a renewal months later (which
//     arrives with a different payment ref and would otherwise look like a fresh payout) is
//     short-circuited.
//   - the (tenant_id, type, ref_id) existence check: redelivery dedup for THIS payment, for
//     the window before the flag is committed.
func (s *CreditService) EarnReferralBonus(ctx context.Context, referrerTenantID, referredTenantID uuid.UUID, paymentAmountKes int, paymentRefID uuid.UUID) error {
	bonusKes := int(float64(paymentAmountKes) * s.referralBonusRate(ctx))
	if bonusKes <= 0 {
		return nil
	}

	wallet, err := s.ensureWallet(ctx, referrerTenantID)
	if err != nil {
		return err
	}

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin referral bonus tx: %w", err)
	}

	// One-time gate: has this referred tenant already produced its referral payout?
	referredSub, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(referredTenantID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("load referred tenant subscription: %w", err)
	}
	if referredSub.ReferralBonusPaid {
		_ = tx.Rollback()
		s.log.Debug("referral bonus already paid for this referred tenant, skipping",
			zap.String("referred_tenant_id", referredTenantID.String()),
		)
		return nil
	}

	// Redelivery dedup: skip if this exact payment already produced a bonus for this referrer.
	already, err := tx.SubscriptionCreditTransaction.Query().
		Where(
			subscriptioncredittransaction.TenantIDEQ(referrerTenantID),
			subscriptioncredittransaction.TypeEQ(subscriptioncredittransaction.TypeReferralBonus),
			subscriptioncredittransaction.RefIDEQ(paymentRefID),
		).
		Exist(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("check referral bonus idempotency: %w", err)
	}
	if already {
		_ = tx.Rollback()
		return nil
	}

	if err := s.addCreditsTx(ctx, tx, wallet.ID, referrerTenantID, bonusKes, "referral_bonus", paymentRefID, "referral"); err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			// The partial unique index caught a concurrent payout for this same payment ref
			// that committed between our Exist() check and this insert. Already credited.
			s.log.Info("referral bonus already credited for this payment, skipping",
				zap.String("referrer_tenant_id", referrerTenantID.String()),
				zap.String("payment_ref_id", paymentRefID.String()),
			)
			return nil
		}
		return err
	}

	// Same transaction as the credit: the payout and "already paid" can never disagree.
	if _, err := tx.TenantSubscription.UpdateOneID(referredSub.ID).
		SetReferralBonusPaid(true).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("mark referral bonus paid: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit referral bonus tx: %w", err)
	}

	s.log.Info("referral bonus credited (one-time, first payment)",
		zap.String("referrer_tenant_id", referrerTenantID.String()),
		zap.String("referred_tenant_id", referredTenantID.String()),
		zap.Int("bonus_kes", bonusKes),
	)
	return nil
}

// GetTransactions returns recent credit transactions for a tenant (newest first, limit 50).
func (s *CreditService) GetTransactions(ctx context.Context, tenantID uuid.UUID) ([]*ent.SubscriptionCreditTransaction, error) {
	return s.orm.SubscriptionCreditTransaction.Query().
		Where(subscriptioncredittransaction.TenantIDEQ(tenantID)).
		Order(ent.Desc("created_at")).
		Limit(50).
		All(ctx)
}

// ensureWallet returns the existing credit wallet or creates one with default settings.
func (s *CreditService) ensureWallet(ctx context.Context, tenantID uuid.UUID) (*ent.SubscriptionCredit, error) {
	wallet, err := s.orm.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(tenantID)).
		Only(ctx)
	if err == nil {
		return wallet, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query wallet: %w", err)
	}

	// Create wallet with defaults
	wallet, err = s.orm.SubscriptionCredit.Create().
		SetTenantID(tenantID).
		SetBalanceKes(0).
		SetLifetimeEarnedKes(0).
		SetLoyaltyRate(0.02).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create wallet: %w", err)
	}

	s.log.Info("credit wallet created", zap.String("tenant_id", tenantID.String()))
	return wallet, nil
}
