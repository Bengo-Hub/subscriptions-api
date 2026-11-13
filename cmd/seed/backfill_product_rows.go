package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// applyMissingPlanProductsEnv turns the missing plan products backfill from a dry run (log only)
// into a real insert. Same switch as the -apply-missing-plan-products flag.
const applyMissingPlanProductsEnv = "SEED_APPLY_MISSING_PLAN_PRODUCTS"

var applyMissingPlanProductsFlag = flag.Bool("apply-missing-plan-products", false,
	"insert the plan-derived default product rows that ACTIVE/TRIAL subscriptions are missing (default: dry run, log only; env "+applyMissingPlanProductsEnv+"=true does the same)")

// applyMissingPlanProducts reports whether the owner switched the missing plan products backfill
// on, by flag or env. Anything else (unset, false, unparsable) keeps it a dry run.
func applyMissingPlanProducts() bool {
	if *applyMissingPlanProductsFlag {
		return true
	}
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(applyMissingPlanProductsEnv)))
	return err == nil && v
}

func seedLogger() *zap.Logger {
	logger, err := zap.NewProduction()
	if err != nil {
		return zap.NewNop()
	}
	return logger
}

// backfillZeroProductRows gives every ACTIVE/TRIAL tenant subscription with zero product rows its
// plan-derived default products (subscriptions.BackfillZeroProductSubscriptions). It runs after
// the seed transaction has committed, so the products and plan features it reads are current, and
// a failure here only logs: it never rolls back the catalog seed.
func backfillZeroProductRows(ctx context.Context, client *ent.Client) {
	logger := seedLogger()
	defer func() { _ = logger.Sync() }()
	res, err := subscriptions.BackfillZeroProductSubscriptions(ctx, client, logger)
	if err != nil {
		log.Printf("zero-row product backfill skipped: %v", err)
		return
	}
	log.Printf("zero-row product backfill: %d subscriptions without product rows, %d filled, %d rows inserted, %d failed",
		res.Subscriptions, res.Filled, res.RowsInserted, res.Failed)
}

// backfillMissingPlanProducts finds ACTIVE/TRIAL subscriptions that have product rows but lack
// some of their plan-derived defaults (subscriptions.BackfillMissingPlanDefaultProducts). Every
// deploy runs it as a dry run that only logs, per tenant, the codes it would insert; it inserts
// only with -apply-missing-plan-products or SEED_APPLY_MISSING_PLAN_PRODUCTS=true, once the owner
// has approved the list. Existing rows are never changed or removed. A failure only logs.
func backfillMissingPlanProducts(ctx context.Context, client *ent.Client) {
	apply := applyMissingPlanProducts()
	logger := seedLogger()
	defer func() { _ = logger.Sync() }()
	res, err := subscriptions.BackfillMissingPlanDefaultProducts(ctx, client, logger, apply)
	if err != nil {
		log.Printf("missing plan products backfill skipped: %v", err)
		return
	}
	mode := "DRY RUN, nothing inserted (set -apply-missing-plan-products or " + applyMissingPlanProductsEnv + "=true to insert)"
	if apply {
		mode = "applied"
	}
	for _, m := range res.Missing {
		log.Printf("missing plan products: tenant %s (%s) plan %s: %s",
			m.TenantSlug, m.TenantID, m.PlanCode, strings.Join(m.Codes, ","))
	}
	log.Printf("missing plan products backfill (%s): %d subscriptions checked, %d missing rows, %d rows inserted, %d failed",
		mode, res.Subscriptions, len(res.Missing), res.RowsInserted, res.Failed)
}
