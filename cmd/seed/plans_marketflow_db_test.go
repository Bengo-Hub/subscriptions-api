package main

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL points at a disposable database), each run in
// its own schema that is dropped afterwards, for example:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/subscriptions_test?sslmode=disable
func newSeedTestClient(t *testing.T) *ent.Client {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "sd_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", url+sep+"search_path="+schemaName)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	return client
}

// seedMarketflowOnce runs the catalog and the MarketFlow plan seed in one committed transaction,
// the same order runSeed uses on every pod start.
func seedMarketflowOnce(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedFeatureCatalog(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := seedMarketflowPlans(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := seedUseCasePowerSuitePlans(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Running the seed twice leaves one feature_definitions row per Autopilot code and the plan links
// of the owner decision of 2026-11-14, with no duplicate plan_features rows.
func TestMarketflowAutopilotSeedIsIdempotent(t *testing.T) {
	client := newSeedTestClient(t)
	ctx := context.Background()
	seedMarketflowOnce(t, client)
	seedMarketflowOnce(t, client)

	for _, code := range []string{"marketflow_autopilot", "autopilot_auto_publish", "max_autopilot_agents"} {
		defs, err := client.FeatureDefinition.Query().Where(featuredefinition.FeatureCodeEQ(code)).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 {
			t.Fatalf("%s: %d feature_definitions rows, want 1", code, len(defs))
		}
		d := defs[0]
		if d.ServiceTag != "marketflow" || d.Description == "" || !d.IsActive {
			t.Errorf("%s: service_tag=%q description=%q active=%v", code, d.ServiceTag, d.Description, d.IsActive)
		}
		wantKind := featuredefinition.KindFEATURE
		if code == "max_autopilot_agents" {
			wantKind = featuredefinition.KindLIMIT
		}
		if d.Kind != wantKind {
			t.Errorf("%s: kind %s, want %s", code, d.Kind, wantKind)
		}
	}

	wantAgents := map[string]float64{"MARKETFLOW_LITE": 1, "MARKETFLOW_GROWTH": 3, "MARKETFLOW_PRO": 10, "MARKETFLOW_BUSINESS": -1}
	for _, tier := range marketflowTiers {
		plans, err := client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(tier.code)).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(plans) != 1 {
			t.Fatalf("%s: %d plans, want 1", tier.code, len(plans))
		}
		p := plans[0]
		rows, err := client.PlanFeature.Query().Where(planfeature.PlanIDEQ(p.ID)).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		count := map[string]int{}
		var codes []string
		for _, r := range rows {
			count[r.FeatureCode]++
			codes = append(codes, r.FeatureCode)
		}
		for code, n := range count {
			if n != 1 {
				t.Errorf("%s: %d plan_features rows for %s, want 1", tier.code, n, code)
			}
		}
		if got, want := slices.Contains(codes, "marketflow_autopilot"), tier.tier >= 2; got != want {
			t.Errorf("%s: marketflow_autopilot linked=%v, want %v", tier.code, got, want)
		}
		if got, want := slices.Contains(codes, "autopilot_auto_publish"), tier.tier >= 4; got != want {
			t.Errorf("%s: autopilot_auto_publish linked=%v, want %v", tier.code, got, want)
		}
		got, has := p.TierLimitsJSON["max_autopilot_agents"]
		want, wantHas := wantAgents[tier.code]
		if has != wantHas {
			t.Errorf("%s: max_autopilot_agents present=%v, want %v", tier.code, has, wantHas)
			continue
		}
		if has {
			if n, ok := got.(float64); !ok || n != want {
				t.Errorf("%s: max_autopilot_agents=%v, want %v", tier.code, got, want)
			}
		}
	}

	// PowerSuite (owner, 2026-11-14): Professional matches MarketFlow Growth, Gold matches
	// MarketFlow Business, Basic has no CRM and so no Autopilot. Never AI credits.
	type psWant struct {
		autopilot, autoPublish bool
		agents                 float64
		hasAgents              bool
	}
	psWants := map[string]psWant{"BASIC": {}, "PRO": {true, false, 3, true}, "GOLD": {true, true, -1, true}}
	for _, fam := range useCaseFamilies {
		for tierCode, w := range psWants {
			code := "POWERSUITE_" + fam.code + "_" + tierCode
			plans, err := client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(code)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(plans) != 1 {
				t.Fatalf("%s: %d plans, want 1", code, len(plans))
			}
			p := plans[0]
			rows, err := client.PlanFeature.Query().Where(planfeature.PlanIDEQ(p.ID)).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			count := map[string]int{}
			for _, r := range rows {
				count[r.FeatureCode]++
			}
			for c, n := range count {
				if n != 1 {
					t.Errorf("%s: %d plan_features rows for %s, want 1", code, n, c)
				}
			}
			if got := count["marketflow_autopilot"] == 1; got != w.autopilot {
				t.Errorf("%s: marketflow_autopilot linked=%v, want %v", code, got, w.autopilot)
			}
			if got := count["autopilot_auto_publish"] == 1; got != w.autoPublish {
				t.Errorf("%s: autopilot_auto_publish linked=%v, want %v", code, got, w.autoPublish)
			}
			got, has := p.TierLimitsJSON["max_autopilot_agents"]
			if has != w.hasAgents {
				t.Errorf("%s: max_autopilot_agents present=%v, want %v", code, has, w.hasAgents)
			} else if has {
				if n, ok := got.(float64); !ok || n != w.agents {
					t.Errorf("%s: max_autopilot_agents=%v, want %v", code, got, w.agents)
				}
			}
			if _, ok := p.TierLimitsJSON["ai_credits_monthly"]; ok {
				t.Errorf("%s: carries ai_credits_monthly; PowerSuite never includes AI credits", code)
			}
		}
	}
}
