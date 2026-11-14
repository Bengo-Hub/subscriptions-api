package subscriptions

import (
	"context"
	"testing"

	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set). G8 of the 2026-11-14 billing audit:
// after a plan switch the old plan's products the new plan does not include are switched off;
// overlay plans, paid add-ons and rows switched off by hand are left alone; a move back restores
// what the switch took away.
func TestSyncPlanProductsFollowsThePlan(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	ctx := context.Background()
	suite := activationPlan(t, client, "POWERSUITE_DUKA_PRO", "pos", nil,
		"pos_terminal", "stock_tracking", "basic_treasury_access")
	treasury := treasuryOnlyPlan(t, client)
	// An admin overlay plan that brings ordering and inventory (boi-style composite tenant).
	overlay := activationPlan(t, client, "ORDERING_OVERLAY", "ordering", nil, "stock_tracking")

	sub := activationSub(t, client, suite)
	if _, err := EnsureMissingPlanDefaultProducts(ctx, client, sub.ID); err != nil {
		t.Fatal(err)
	}
	addProductRow(t, client, sub.ID, "whatsapp_messaging", productsubscription.StatusActive)
	addProductRow(t, client, sub.ID, "ordering", productsubscription.StatusActive)
	if _, err := client.ProductSubscription.Update().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID), productsubscription.ProductCodeEQ("ordering")).
		SetOverridePlanID(overlay.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}

	// PowerSuite to treasury only.
	if _, err := client.TenantSubscription.UpdateOneID(sub.ID).SetPlanID(treasury.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	off, on, err := SyncPlanProducts(ctx, client, sub.ID, suite.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedJoin(off); got != "pos" || len(on) != 0 {
		t.Fatalf("switched off %q on %q, want only pos off (inventory is kept by the overlay)", got, sortedJoin(on))
	}
	for code, want := range map[string]productsubscription.Status{
		"pos": productsubscription.StatusInactive, "inventory": productsubscription.StatusActive,
		"treasury": productsubscription.StatusActive, "ordering": productsubscription.StatusActive,
		"whatsapp_messaging": productsubscription.StatusActive, "auth": productsubscription.StatusActive,
	} {
		if st := productRowStatus(t, client, sub.ID, code); st != want {
			t.Fatalf("%s = %s, want %s", code, st, want)
		}
	}

	// Back to PowerSuite: pos is switched on again.
	if _, err := client.TenantSubscription.UpdateOneID(sub.ID).SetPlanID(suite.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	off, on, err = SyncPlanProducts(ctx, client, sub.ID, treasury.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sortedJoin(on) != "pos" || len(off) != 0 {
		t.Fatalf("move back: on %v off %v, want pos back on", on, off)
	}
	if st := productRowStatus(t, client, sub.ID, "pos"); st != productsubscription.StatusActive {
		t.Fatalf("pos = %s after the move back", st)
	}
}

// A product the tenant or an admin switched off (no plan-change marker) stays off on any move.
func TestSyncPlanProductsLeavesHandDeactivatedRows(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	ctx := context.Background()
	suite := activationPlan(t, client, "POWERSUITE_DUKA_PRO", "pos", nil, "pos_terminal", "stock_tracking")
	treasury := treasuryOnlyPlan(t, client)
	sub := activationSub(t, client, treasury)
	addProductRow(t, client, sub.ID, "pos", productsubscription.StatusInactive)
	if _, err := client.TenantSubscription.UpdateOneID(sub.ID).SetPlanID(suite.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SyncPlanProducts(ctx, client, sub.ID, treasury.ID); err != nil {
		t.Fatal(err)
	}
	if st := productRowStatus(t, client, sub.ID, "pos"); st != productsubscription.StatusInactive {
		t.Fatalf("pos = %s, a hand-deactivated row must stay off", st)
	}
}
