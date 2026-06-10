package subscriptions

import (
	"context"
	"crypto/rand"

	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
)

// referralCodeAlphabet excludes visually ambiguous characters (0/O, 1/I/L) so codes are easy
// to read and share.
const referralCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// generateReferralCode returns a short, human-shareable referral code, e.g. "CV-7QK9MZ4P".
func generateReferralCode() string {
	const n = 8
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// rand.Read essentially never fails; fall back to a UUID-derived code if it does.
		return "CV-" + uuid.NewString()[:8]
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = referralCodeAlphabet[int(v)%len(referralCodeAlphabet)]
	}
	return "CV-" + string(out)
}

// resolveReferrer returns the tenant that owns the given referral code, if any.
func (s *Service) resolveReferrer(ctx context.Context, code string) (uuid.UUID, bool) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.ReferralCodeEQ(code)).
		Only(ctx)
	if err != nil {
		return uuid.Nil, false
	}
	return sub.TenantID, true
}

// GetOrCreateReferralCode returns the calling tenant's shareable referral code, generating and
// persisting one if the subscription predates referral support.
func (s *Service) GetOrCreateReferralCode(ctx context.Context, tenantID uuid.UUID) (string, error) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return "", err
	}
	if sub.ReferralCode != nil && *sub.ReferralCode != "" {
		return *sub.ReferralCode, nil
	}
	code := generateReferralCode()
	if _, err := s.client.TenantSubscription.UpdateOneID(sub.ID).SetReferralCode(code).Save(ctx); err != nil {
		return "", err
	}
	return code, nil
}
