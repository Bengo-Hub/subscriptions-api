package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredit"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredittransaction"
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

	newBalance := wallet.BalanceKes + amountKes
	newLifetime := wallet.LifetimeEarnedKes + amountKes

	_, err = s.orm.SubscriptionCredit.UpdateOneID(wallet.ID).
		SetBalanceKes(newBalance).
		SetLifetimeEarnedKes(newLifetime).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}

	b := s.orm.SubscriptionCreditTransaction.Create().
		SetTenantID(tenantID).
		SetCreditID(wallet.ID).
		SetType(subscriptioncredittransaction.Type(txType)).
		SetAmountKes(amountKes).
		SetNillableRefID(&refID).
		SetNillableRefType(&refType)
	if _, err := b.Save(ctx); err != nil {
		return fmt.Errorf("record credit transaction: %w", err)
	}

	s.log.Info("credits added",
		zap.String("tenant_id", tenantID.String()),
		zap.Int("amount_kes", amountKes),
		zap.String("type", txType),
	)
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

	return s.AddCredits(ctx, tenantID, earnKes, "earned", paymentRefID, "payment")
}

// ReferralBonusRate is the share of a referred tenant's payment credited to the referrer's
// subscription wallet (Type-A referral reward). 0.10 = 10%.
const ReferralBonusRate = 0.10

// EarnReferralBonus credits a Type-A referral bonus to the REFERRER's wallet when one of the
// tenants they referred makes a successful subscription payment. The bonus is a share of that
// payment. Idempotent on paymentRefID so NATS redelivery cannot double-credit.
func (s *CreditService) EarnReferralBonus(ctx context.Context, referrerTenantID, referredTenantID uuid.UUID, paymentAmountKes int, paymentRefID uuid.UUID) error {
	bonusKes := int(float64(paymentAmountKes) * ReferralBonusRate)
	if bonusKes <= 0 {
		return nil
	}

	// Idempotency: skip if this payment already produced a referral bonus for this referrer.
	already, err := s.orm.SubscriptionCreditTransaction.Query().
		Where(
			subscriptioncredittransaction.TenantIDEQ(referrerTenantID),
			subscriptioncredittransaction.TypeEQ(subscriptioncredittransaction.TypeReferralBonus),
			subscriptioncredittransaction.RefIDEQ(paymentRefID),
		).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("check referral bonus idempotency: %w", err)
	}
	if already {
		return nil
	}

	if err := s.AddCredits(ctx, referrerTenantID, bonusKes, "referral_bonus", paymentRefID, "referral"); err != nil {
		return err
	}
	s.log.Info("referral bonus credited",
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
