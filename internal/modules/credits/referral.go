package credits

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// Gift a friend (owner, 2026-11-12): when a business invited by another tenant makes its first
// payment of any kind, the inviter and the friend each get a one time AI credit reward. An
// inviter is rewarded for at most a set number of friends in all. Both numbers are platform
// service_configs; the defaults below are the owner's terms. The existing KES subscription
// credit bonus for the inviter (billing.CreditService.EarnReferralBonus) is separate and stays.
const (
	ReferralAIRewardConfigKey = "subscriptions.referral_ai_reward_credits"
	ReferralAICapConfigKey    = "subscriptions.referral_ai_invite_cap"
	DefaultReferralAIReward   = 5
	DefaultReferralAICap      = 10
	// MaxReferralAIReward bounds a mistyped setting; the owner's terms say at most 5.
	MaxReferralAIReward = 5
)

// referralKey is the idempotency key of one side of one friend's reward.
func referralKey(friend uuid.UUID, side string) string {
	return "referral-ai:" + friend.String() + ":" + side
}

// referralInviterLike matches every inviter-side reward key, for the cap.
const referralInviterLike = "referral-ai:%:inviter"

// ReferralTerms are the reward and the cap in force.
type ReferralTerms struct {
	RewardCredits int64 `json:"reward_credits"`
	InviteCap     int   `json:"invite_cap"`
}

// ReferralTerms reads the reward and the cap. A missing or bad value keeps its default; a
// reward above MaxReferralAIReward is held to it.
func (s *Service) ReferralTerms(ctx context.Context) ReferralTerms {
	t := ReferralTerms{RewardCredits: DefaultReferralAIReward, InviteCap: DefaultReferralAICap}
	if v, ok := s.platformInt(ctx, ReferralAIRewardConfigKey); ok && v >= 0 {
		t.RewardCredits = min(int64(v), MaxReferralAIReward)
	}
	if v, ok := s.platformInt(ctx, ReferralAICapConfigKey); ok && v >= 0 {
		t.InviteCap = v
	}
	return t
}

func (s *Service) platformInt(ctx context.Context, key string) (int, bool) {
	cfg, err := s.orm.ServiceConfig.Query().
		Where(serviceconfig.ConfigKeyEQ(key), serviceconfig.TenantIDIsNil()).Only(ctx)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(cfg.ConfigValue))
	return v, err == nil
}

// ReferralReward is what one call to GrantReferralReward paid.
type ReferralReward struct {
	Inviter, Friend int64
	Capped          bool
}

// GrantReferralReward pays the inviter and the friend their AI credit reward for the friend's
// first payment. It is safe to call on every payment: each side is paid at most once per
// friend (idempotency keys), and the inviter's cap is checked under the wallet lock. Past the
// cap neither side is paid, since the invitation itself is what the cap limits.
func (s *Service) GrantReferralReward(ctx context.Context, inviter, friend uuid.UUID) (ReferralReward, error) {
	var out ReferralReward
	if inviter == uuid.Nil || friend == uuid.Nil || inviter == friend {
		return out, nil
	}
	terms := s.ReferralTerms(ctx)
	if terms.RewardCredits <= 0 || terms.InviteCap <= 0 {
		return out, nil
	}
	spec, ok := billing.LookupMeteredService(billing.ServiceAI)
	if !ok {
		return out, fmt.Errorf("ai credit service is not registered")
	}
	grant := func(tenant uuid.UUID, side, desc string, capped bool) (bool, error) {
		if err := s.ensureActivated(ctx, tenant, spec, nil); err != nil {
			return false, err
		}
		req := billing.CreditRequest{TenantID: tenant, ServiceTag: spec.BundleTag, Units: terms.RewardCredits,
			Action: "grant", Key: referralKey(friend, side), RefID: friend, RefType: "referral", Description: desc}
		if capped {
			req.CapKeyLike, req.CapCount = referralInviterLike, terms.InviteCap
		}
		_, applied, err := s.wallets.Credit(ctx, req)
		return applied, err
	}
	applied, err := grant(inviter, "inviter", "Invite reward: a business you invited made its first payment", true)
	switch {
	case errors.Is(err, billing.ErrCreditCapReached):
		out.Capped = true
		return out, nil
	case err != nil:
		return out, fmt.Errorf("inviter reward: %w", err)
	}
	if applied {
		out.Inviter = terms.RewardCredits
	}
	applied, err = grant(friend, "friend", "Welcome reward: you joined through an invitation", false)
	if err != nil {
		return out, fmt.Errorf("friend reward: %w", err)
	}
	if applied {
		out.Friend = terms.RewardCredits
	}
	return out, nil
}

// ReferralFriend is one business the tenant invited.
type ReferralFriend struct {
	Name     string    `json:"name"`
	JoinedAt time.Time `json:"joined_at"`
	Rewarded bool      `json:"rewarded"`
}

// ReferralSummary is what the tenant's "Invite and earn" page shows.
type ReferralSummary struct {
	Code          string `json:"referral_code"`
	RewardCredits int64  `json:"reward_credits"`
	InviteCap     int    `json:"invite_cap"`
	RewardsUsed   int    `json:"rewards_used"`
	RewardsLeft   int    `json:"rewards_left"`
	CreditsEarned int64  `json:"credits_earned"`
	// CreditValueKES is what one AI credit costs in the smallest bundle on sale: the honest
	// "worth about" of a reward. The page converts it to USD with the platform rate.
	CreditValueKES float64          `json:"credit_value_kes"`
	Friends        []ReferralFriend `json:"friends"`
}

// maxReferralFriendsListed bounds the list on the page.
const maxReferralFriendsListed = 100

// ReferralSummary reads the tenant's code, terms, progress and invited businesses.
// The code is the tenant's subscription referral code (created on first read).
func (s *Service) ReferralSummary(ctx context.Context, tenantID uuid.UUID) (ReferralSummary, error) {
	code, err := s.subs.GetOrCreateReferralCode(ctx, tenantID)
	if err != nil {
		return ReferralSummary{}, fmt.Errorf("referral code: %w", err)
	}
	terms := s.ReferralTerms(ctx)
	out := ReferralSummary{Code: code, RewardCredits: terms.RewardCredits, InviteCap: terms.InviteCap, Friends: []ReferralFriend{}}
	if spec, ok := billing.LookupMeteredService(billing.ServiceAI); ok {
		for _, b := range spec.OfferedBundles() {
			if b.Units > 0 {
				if v := b.PriceKES / float64(b.Units); v > out.CreditValueKES {
					out.CreditValueKES = v
				}
			}
		}
	}
	subs, err := s.orm.TenantSubscription.Query().
		Where(tenantsubscription.ReferredByEQ(tenantID)).
		Order(ent.Desc(tenantsubscription.FieldCreatedAt)).
		Limit(maxReferralFriendsListed).
		WithTenant().
		All(ctx)
	if err != nil {
		return out, fmt.Errorf("invited businesses: %w", err)
	}
	rewarded, err := s.wallets.CreditKeysLike(ctx, tenantID, billing.ServiceAI, referralInviterLike)
	if err != nil {
		return out, fmt.Errorf("referral rewards: %w", err)
	}
	for _, sub := range subs {
		f := ReferralFriend{JoinedAt: sub.CreatedAt, Rewarded: rewarded[referralKey(sub.TenantID, "inviter")]}
		if t := sub.Edges.Tenant; t != nil {
			f.Name = t.Name
		}
		out.Friends = append(out.Friends, f)
	}
	out.RewardsUsed = len(rewarded)
	out.RewardsLeft = max(0, terms.InviteCap-out.RewardsUsed)
	out.CreditsEarned = int64(out.RewardsUsed) * terms.RewardCredits
	return out, nil
}
