package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bengobox/subscription-service/internal/ent"
)

// validateCatalogLinkage is the strict, fail-closed gate that keeps the feature catalog the
// single source of truth. After all plans + features are seeded it asserts that EVERY plan
// feature code and EVERY tier-limit key resolves to a catalog entry (via canonicalFeatureCode).
// Any unknown code aborts the seed with a precise report instead of silently shipping a code
// the backends/UI gate on but the catalog never defined (the drift that breaks gating).
func validateCatalogLinkage(ctx context.Context, tx *ent.Tx) error {
	// Unknown plan feature codes (covers included + addon PlanFeature rows).
	feats, err := tx.PlanFeature.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query plan features for validation: %w", err)
	}
	unknownFeatures := map[string]bool{}
	for _, f := range feats {
		if !isInCatalog(f.FeatureCode) {
			unknownFeatures[f.FeatureCode] = true
		}
	}

	// Unknown tier-limit keys across all plans.
	plans, err := tx.SubscriptionPlan.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query plans for validation: %w", err)
	}
	unknownLimits := map[string]bool{}
	for _, p := range plans {
		for k := range p.TierLimitsJSON {
			if _, ok := limitServiceTag(k); !ok {
				unknownLimits[k] = true
			}
		}
	}

	if len(unknownFeatures) == 0 && len(unknownLimits) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("feature catalog drift detected — add these to featureCatalog in cmd/seed/feature_catalog.go (or map via featureCodeAliases):\n")
	if len(unknownFeatures) > 0 {
		b.WriteString("  unknown FEATURE codes: ")
		b.WriteString(strings.Join(sortedKeys(unknownFeatures), ", "))
		b.WriteString("\n")
	}
	if len(unknownLimits) > 0 {
		b.WriteString("  unknown LIMIT keys: ")
		b.WriteString(strings.Join(sortedKeys(unknownLimits), ", "))
		b.WriteString("\n")
	}
	return fmt.Errorf("%s", b.String())
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
