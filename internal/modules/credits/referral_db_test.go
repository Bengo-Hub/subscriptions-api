package credits

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

func (f *fixture) aiBalance(t *testing.T, tenant uuid.UUID) int64 {
	t.Helper()
	var b int64
	err := f.db.QueryRow(`SELECT balance FROM api_token_wallets WHERE tenant_id = $1 AND service_tag = $2`, tenant, billing.ServiceAI).Scan(&b)
	if err != nil {
		return 0
	}
	return b
}

func (f *fixture) setConfig(t *testing.T, key, value string) {
	t.Helper()
	if err := f.orm.ServiceConfig.Create().SetConfigKey(key).SetConfigValue(value).SetConfigType("int").Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Gift a friend (owner, 2026-11-12): the inviter and the friend each get 5 AI credits once per
// friend, and an inviter is rewarded for at most 10 friends in all.
func TestReferralRewardOncePerFriend(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	inviter, friend := f.tenantID, uuid.New()

	got, err := f.svc.GrantReferralReward(ctx, inviter, friend)
	if err != nil || got.Inviter != DefaultReferralAIReward || got.Friend != DefaultReferralAIReward {
		t.Fatalf("first payment: %+v %v", got, err)
	}
	// A renewal or a redelivered event pays nothing more.
	got, err = f.svc.GrantReferralReward(ctx, inviter, friend)
	if err != nil || got.Inviter != 0 || got.Friend != 0 {
		t.Fatalf("second payment: %+v %v", got, err)
	}
	if b := f.aiBalance(t, inviter); b != DefaultReferralAIReward {
		t.Fatalf("inviter balance %d", b)
	}
	if b := f.aiBalance(t, friend); b != DefaultReferralAIReward {
		t.Fatalf("friend balance %d", b)
	}
	if got, err := f.svc.GrantReferralReward(ctx, inviter, inviter); err != nil || got.Inviter != 0 {
		t.Fatalf("a tenant cannot reward itself: %+v %v", got, err)
	}
}

// The cap holds even when friends pay at the same moment, and past it neither side is paid.
func TestReferralRewardCapHoldsUnderRace(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.setConfig(t, ReferralAICapConfigKey, "3")
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	paid, capped := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := f.svc.GrantReferralReward(ctx, f.tenantID, uuid.New())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if got.Capped {
				capped++
				if got.Friend != 0 {
					t.Error("a friend past the cap was paid")
				}
			} else if got.Inviter > 0 {
				paid++
			}
		}()
	}
	wg.Wait()
	if paid != 3 || capped != 5 {
		t.Fatalf("paid %d capped %d, want 3 and 5", paid, capped)
	}
	if b := f.aiBalance(t, f.tenantID); b != 3*DefaultReferralAIReward {
		t.Fatalf("inviter balance %d", b)
	}
}

// A reward set above the owner's limit is held to it; zero turns rewards off.
func TestReferralTermsFromConfig(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.setConfig(t, ReferralAIRewardConfigKey, "50")
	if terms := f.svc.ReferralTerms(context.Background()); terms.RewardCredits != MaxReferralAIReward || terms.InviteCap != DefaultReferralAICap {
		t.Fatalf("terms %+v", terms)
	}
}
