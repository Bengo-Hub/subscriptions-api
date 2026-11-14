package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/credits"
)

// applyClearIncludedAIEnv turns the one-time clear of included AI credits on plans that grant none
// (the six PowerSuite tenants left with an old allowance, audit 2026-11-14) from a dry run into a
// real write. Same switch as the -apply-clear-included-ai flag.
const applyClearIncludedAIEnv = "SEED_APPLY_CLEAR_INCLUDED_AI"

var applyClearIncludedAIFlag = flag.Bool("apply-clear-included-ai", false,
	"set the included AI credit allowance (ai_credits_quota) to 0 for tenants whose plan grants none, one ledger row each; bought credits are never touched (default: dry run, log only; env "+applyClearIncludedAIEnv+"=true does the same)")

func applyClearIncludedAI() bool {
	if *applyClearIncludedAIFlag {
		return true
	}
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(applyClearIncludedAIEnv)))
	return err == nil && v
}

// clearIncludedAIAllowances runs credits.ClearUngrantedAIAllowances after the seed transaction has
// committed (so plan limits are current). Every deploy runs it as a dry run that only logs; the
// owner switches the write on once. Idempotent: a cleared wallet carries the fix's key and is
// never written again, and a failure only logs.
func clearIncludedAIAllowances(ctx context.Context, client *ent.Client, db *sql.DB) {
	logger := seedLogger()
	defer func() { _ = logger.Sync() }()
	apply := applyClearIncludedAI()
	res, err := credits.ClearUngrantedAIAllowances(ctx, client, billing.NewTokenWalletService(logger, client, db), logger, apply)
	if err != nil {
		log.Printf("clear included AI credits skipped: %v", err)
		return
	}
	mode := "dry run"
	if apply {
		mode = "applied"
	}
	for _, f := range res.Found {
		log.Printf("clear included AI credits (%s): %s on %s holds %d", mode, f.Slug, f.PlanCode, f.Balance)
	}
	log.Printf("clear included AI credits (%s): %d found, %d cleared, %d failed", mode, len(res.Found), res.Cleared, res.Failed)
}
