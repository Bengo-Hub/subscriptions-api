package consumers

import "testing"

// Completed POS sales count as orders (drafts and open orders never reach pos.sale.finalized),
// except online orders, which ordering.order.created already counted.
func TestSaleFinalizedCountsOrders(t *testing.T) {
	m, ok := usageSubjectMappings["pos.sale.finalized"]
	if !ok {
		t.Fatal("pos.sale.finalized is not metered")
	}
	if m.metric != "transactions" || len(m.extraMetrics) != 1 || m.extraMetrics[0] != "orders" {
		t.Fatalf("mapping %+v", m)
	}
	if !containsFold(m.skipExtraForSources, "online_ordering") || !containsFold(m.skipExtraForSources, "ONLINE_ORDERING") {
		t.Fatal("online orders must not be counted twice")
	}
	if containsFold(m.skipExtraForSources, "pos") || containsFold(m.skipExtraForSources, "") {
		t.Fatal("till sales must count as orders")
	}
	if _, ok := usageSubjectMappings["ordering.order.created"]; !ok {
		t.Fatal("online orders are counted on ordering.order.created")
	}
}
