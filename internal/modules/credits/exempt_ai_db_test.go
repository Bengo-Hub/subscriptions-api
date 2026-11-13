package credits

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// Owner, 2026-11-12: paid AI work is free only for the platform owner's own tenant. The demo
// sandbox's password is public, so it pays for AI like anyone (it has no subscription, so AI
// spends are refused); it stays exempt for the other services as before.
func TestOnlyThePlatformTenantGetsFreeAI(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	mk := func(slug string) uuid.UUID {
		t.Helper()
		tn, err := f.orm.Tenant.Create().SetID(uuid.New()).SetName(slug).SetSlug(slug).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return tn.ID
	}
	platform, demo := mk("codevertex"), mk("codevertex-demo")
	ai, _ := billing.LookupMeteredService(billing.ServiceAI)
	wa, _ := billing.LookupMeteredService(billing.ServiceWhatsApp)

	if cov, err := f.svc.Coverage(ctx, platform, ai); err != nil || !cov.Exempt {
		t.Fatalf("platform tenant AI: %+v %v", cov, err)
	}
	if cov, err := f.svc.Coverage(ctx, demo, ai); err != nil || cov.Exempt || cov.Covered {
		t.Fatalf("demo tenant AI must not be free: %+v %v", cov, err)
	}
	if cov, err := f.svc.Coverage(ctx, demo, wa); err != nil || !cov.Exempt {
		t.Fatalf("demo tenant keeps its exemption for other services: %+v %v", cov, err)
	}
	d, err := f.svc.Consume(ctx, ConsumeInput{TenantID: demo, Service: billing.ServiceAI, Units: 5, Key: "demo-ai-1"})
	if err != nil || d.Allowed || d.Exempt {
		t.Fatalf("a demo AI spend is refused: %+v %v", d, err)
	}
	d, err = f.svc.Consume(ctx, ConsumeInput{TenantID: platform, Service: billing.ServiceAI, Units: 5, Key: "platform-ai-1"})
	if err != nil || !d.Allowed || !d.Exempt {
		t.Fatalf("a platform AI spend is free: %+v %v", d, err)
	}
}
