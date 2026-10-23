package recall

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func TestReadTakesChartsPlatformAndLedgerFromTheStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"charts/b/map.md":           "## Fog\n\n- b item\n",
		"charts/a/map.md":           "## Fog\n\n- a item\n",
		"charts/nomap/tickets.yaml": "tickets: []\n",
		"charts/stray.md":           "not a chart",
		"platform/rules.md":         "# Rules\n",
		"ledger.md":                 "# Ledger\n",
	})
	m, err := Read(&store.Store{Root: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Charts) != 2 || m.Charts[0].Name != "a" || m.Charts[1].Name != "b" || !strings.Contains(m.Charts[0].Map, "a item") {
		t.Errorf("charts = %+v, want a and b, in that order, and none for the chart with no map", m.Charts)
	}
	if len(m.Docs) != 1 || m.Docs[0].Path != "platform/rules.md" {
		t.Errorf("docs = %+v, want platform/rules.md", m.Docs)
	}
	if m.Ledger != "# Ledger\n" {
		t.Errorf("ledger = %q", m.Ledger)
	}
}

func TestReadOfABareStoreIsEmptyMaterial(t *testing.T) {
	t.Parallel()
	m, err := Read(&store.Store{Root: t.TempDir()}, "platform/")
	if err != nil || len(m.Charts) != 0 || len(m.Docs) != 0 || m.Ledger != "" {
		t.Errorf("Read of a bare store = %+v, %v, want empty material", m, err)
	}
}
