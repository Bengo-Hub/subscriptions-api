package handlers

import "testing"

func TestSortUsageMetricsIsStableAndUrgentFirst(t *testing.T) {
	metrics := []metricResult{
		{Name: "Users", Used: 2, Limit: 10},      // 20%
		{Name: "Api Calls", Used: 500, Limit: 0}, // unlimited
		{Name: "Orders", Used: 95, Limit: 100},   // 95%
		{Name: "Outlets", Used: 1, Limit: 5},     // 20%, same as Users: name breaks the tie
		{Name: "Reports", Used: 3, Limit: 0},     // unlimited
	}
	sortUsageMetrics(metrics)
	want := []string{"Orders", "Outlets", "Users", "Api Calls", "Reports"}
	for i, name := range want {
		if metrics[i].Name != name {
			t.Fatalf("position %d = %s, want %s (order %v)", i, metrics[i].Name, name, names(metrics))
		}
	}
}

func names(ms []metricResult) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}
