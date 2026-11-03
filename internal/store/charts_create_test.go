package store_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// TestCreateChart: the chart a store makes is read back by ReadChart as no
// entries, by recall.ParseMap as its title with every section empty, and
// listed by ChartNames; ChartRelDir is the folder in the form Claim takes.
// (This file is an external test package because recall imports store.)
func TestCreateChart(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}

	if got, want := st.ChartRelDir("coupons"), filepath.Join("charts", "coupons"); got != want {
		t.Fatalf("ChartRelDir = %q, want %q", got, want)
	}
	if err := st.CreateChart("coupons", "Coupons at checkout"); err != nil {
		t.Fatalf("CreateChart: %v", err)
	}

	entries, err := st.ReadChart("coupons")
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ReadChart = %+v, want no entries", entries)
	}
	names, err := st.ChartNames()
	if err != nil || !reflect.DeepEqual(names, []string{"coupons"}) {
		t.Fatalf("ChartNames = %v, %v, want [coupons]", names, err)
	}

	mapMD, err := os.ReadFile(st.ChartMapFile("coupons"))
	if err != nil {
		t.Fatal(err)
	}
	wantMap := "# Coupons at checkout\n\n## Destination\n\n## Notes\n\n## Decisions so far\n\n## Not yet specified (fog)\n\n## Out of scope\n\n"
	if string(mapMD) != wantMap {
		t.Fatalf("map.md = %q, want %q", mapMD, wantMap)
	}
	sections := recall.ParseMap(string(mapMD))
	want := recall.MapSections{Title: "Coupons at checkout", Destination: []string{}, Decided: []string{}, Fog: []string{}, OutOfScope: []string{}}
	if !reflect.DeepEqual(sections, want) {
		t.Fatalf("ParseMap = %+v, want %+v", sections, want)
	}

	ticketsYAML, err := os.ReadFile(st.ChartFile("coupons"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"# Handover to `jig graduate coupons`: one entry per ticket, in order. An entry has\n",
		"# stacked. jig writes each ticket's id back into its entry when it graduates.\ntickets: []\n",
	} {
		if !strings.Contains(string(ticketsYAML), line) {
			t.Errorf("tickets.yaml lacks %q:\n%s", line, ticketsYAML)
		}
	}
	if !strings.HasSuffix(string(ticketsYAML), "tickets: []\n") {
		t.Errorf("tickets.yaml does not end with the empty list:\n%s", ticketsYAML)
	}
	for _, p := range []string{st.ChartMapFile("coupons"), st.ChartFile("coupons")} {
		entries, err := os.ReadDir(filepath.Dir(p))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("a temporary file is left behind: %s", e.Name())
			}
		}
	}
}

// TestCreateChartRefusesATakenName: an existing charts/<name> in any form, or
// one that differs only in case, is refused and nothing of it is changed.
func TestCreateChartRefusesATakenName(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "Legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "a-file"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateChart("coupons", "Coupons"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(st.ChartMapFile("coupons"))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"coupons", "legacy", "a-file"} {
		err := st.CreateChart(name, "Other title")
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("CreateChart(%q) = %v, want it refused as already existing", name, err)
		}
	}
	after, err := os.ReadFile(st.ChartMapFile("coupons"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("the existing chart's map.md changed: %q, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "charts", "Legacy", "tickets.yaml")); err == nil {
		t.Fatal("a refused name left a tickets.yaml in the folder that differs only in case")
	}
	if taken, err := st.ChartFolderTaken("free-name"); err != nil || taken {
		t.Fatalf("ChartFolderTaken(free-name) = %v, %v, want false", taken, err)
	}
}
