package consumers

import "testing"

// A completed POS sale counts as one transaction and never as an online-ordering order:
// max_orders_per_month meters online ordering only (ordering.order.created).
func TestSaleFinalizedCountsTransactionOnly(t *testing.T) {
	m, ok := usageSubjectMappings["pos.sale.finalized"]
	if !ok {
		t.Fatal("pos.sale.finalized is not metered")
	}
	if m.metric != "transactions" || m.service != "pos" {
		t.Fatalf("mapping %+v", m)
	}
	if len(m.roleMetrics) != 0 || m.amountField != "" || m.delta != 0 {
		t.Fatalf("a sale must add exactly one transaction, got %+v", m)
	}
}

// Only ordering.order.created feeds the orders metric, so a POS sale can never use up the
// online-ordering allowance.
func TestOrdersMeteredOnlyByOnlineOrdering(t *testing.T) {
	var subjects []string
	for subject, m := range usageSubjectMappings {
		if m.metric == "orders" {
			subjects = append(subjects, subject)
		}
		for _, target := range m.roleMetrics {
			if target == "orders" {
				subjects = append(subjects, subject)
			}
		}
	}
	if len(subjects) != 1 || subjects[0] != "ordering.order.created" {
		t.Fatalf("orders metered by %v, want only ordering.order.created", subjects)
	}
}

// One sale is one transaction: exactly one subject may feed the transactions metric,
// otherwise a sale reported on two subjects would be counted twice (pos.transaction.created
// used to sit beside pos.sale.finalized).
func TestTransactionsHaveSingleSource(t *testing.T) {
	var subjects []string
	for subject, m := range usageSubjectMappings {
		if m.metric == "transactions" {
			subjects = append(subjects, subject)
		}
	}
	if len(subjects) != 1 || subjects[0] != "pos.sale.finalized" {
		t.Fatalf("transactions metered by %v, want only pos.sale.finalized", subjects)
	}
	if _, ok := usageSubjectMappings["pos.transaction.created"]; ok {
		t.Fatal("pos.transaction.created must not be metered beside pos.sale.finalized")
	}
}
