package store_test

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestChartNewNeedDestinationNamesTheIssue: a chart made for an issue has one
// line under Destination naming it, and one made without has none.
func TestChartNewNeedDestinationNamesTheIssue(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	if err := st.CreateChartWithIssue("a", "A", "SHOP-3"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateChartWithIssue("b", "B", ""); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"a": "## Destination\n\nPlans issue SHOP-3.\n\n## Notes\n",
		"b": "## Destination\n\n## Notes\n",
	} {
		data, err := os.ReadFile(st.ChartMapFile(name))
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("chart %s map = %q, %v, want it to hold %q", name, data, err, want)
		}
	}
}
