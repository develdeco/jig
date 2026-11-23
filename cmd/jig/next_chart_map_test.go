package main

import (
	"reflect"
	"testing"
)

// TestNextChartMapSkipsTablesAndNone: a table under the decisions is no
// decided item, and "None." under the fog is no fog, so the chart's counts do
// not show an item nobody decided or left open.
func TestNextChartMapSkipsTablesAndNone(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: Apply a coupon code\n",
		"# Coupons at checkout\n\n## Decisions so far\n\n- One coupon per order\n\n| id | decision |\n|----|----------|\n| 1 | codes are case-insensitive |\n\n- Codes expire\n\n## Not yet specified (fog)\n\nNone.\n\n## Out of scope\n\n- Gift cards\n")
	n := r.next(t, "checkout")
	if n.Map == nil {
		t.Fatal("the chart has no map")
	}
	if want := []string{"One coupon per order", "Codes expire"}; !reflect.DeepEqual(n.Map.Decided, want) {
		t.Errorf("map.decided = %q, want %q", n.Map.Decided, want)
	}
	if len(n.Map.Fog) != 0 {
		t.Errorf("map.fog = %q, want none", n.Map.Fog)
	}
	if want := []string{"Gift cards"}; !reflect.DeepEqual(n.Map.OutOfScope, want) {
		t.Errorf("map.out_of_scope = %q, want %q", n.Map.OutOfScope, want)
	}
}
