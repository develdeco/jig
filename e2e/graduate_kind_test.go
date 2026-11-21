package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// kindNextOut is the part of `jig next <ticket> --json` an issue or a
// multi-task prints.
type kindNextOut struct {
	Ticket   string `json:"ticket"`
	Kind     string `json:"kind"`
	Parent   string `json:"parent"`
	Chart    string `json:"chart"`
	Phase    any    `json:"phase"`
	Children []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"children"`
}

// TestGraduateKindFlow drives the model end to end with the real binary: an
// issue, a chart of a task and a multi-task of two tasks that validates and
// graduates with each ticket's kind and parent recorded; an issue and task show
// as such in `jig next`; then a chart with a multi-task under a multi-task is
// listed by `jig validate` and refused by `jig graduate` with nothing minted.
func TestGraduateKindFlow(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(kind string) string {
		before, _ := st.Tickets()
		if r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "A "+kind, "--kind", kind); r.Code != 0 {
			t.Fatalf("jig ticket new --kind %s: exit %d\n%s", kind, r.Code, r.Stdout)
		}
		after, _ := st.Tickets()
		for _, id := range after {
			found := false
			for _, b := range before {
				found = found || b == id
			}
			if !found {
				return id
			}
		}
		t.Fatalf("no ticket minted for kind %s", kind)
		return ""
	}
	issue := mint("issue")

	chartDir := filepath.Join(fx.StoreDir, "charts", "plan")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "tickets.yaml"), []byte("issue: "+issue+`
tickets:
  - title: Group
    kind: multi-task
  - title: First part
    under: "#1"
  - title: Second part
    under: "#1"
    blocked_by:
      - ref: "#2"
  - title: Standalone
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := runJig(t, env, fx.StoreDir, "validate", "plan"); r.Code != 0 {
		t.Fatalf("jig validate plan: exit %d\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.StoreDir, "graduate", "plan"); r.Code != 0 {
		t.Fatalf("jig graduate plan: exit %d\n%s", r.Code, r.Stdout)
	}
	entries, err := st.ReadChart("plan")
	if err != nil {
		t.Fatal(err)
	}
	wantKind := []string{store.KindMultiTask, store.KindTask, store.KindTask, store.KindTask}
	wantParent := []string{"", entries[0].ID, entries[0].ID, ""}
	for i, e := range entries {
		rec, err := st.ReadTicket(e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.KindOrDefault() != wantKind[i] || rec.Parent != wantParent[i] {
			t.Errorf("entry %d %s: kind %q parent %q, want %q %q", i+1, e.ID, rec.KindOrDefault(), rec.Parent, wantKind[i], wantParent[i])
		}
		raw, _ := os.ReadFile(st.TicketFilePath(e.ID))
		if hasKind := strings.Contains(string(raw), "\nkind:"); hasKind != (wantKind[i] != store.KindTask) {
			t.Errorf("entry %d ticket.yaml has a kind key = %v:\n%s", i+1, hasKind, raw)
		}
	}

	// The issue and the multi-task are shown, not briefed: no phase, no prompt.
	group := nextJSON[kindNextOut](t, env, fx.RepoDir, entries[0].ID)
	if group.Kind != store.KindMultiTask || group.Phase != nil || len(group.Children) != 2 {
		t.Errorf("jig next %s = %+v, want a multi-task with 2 children and no phase", entries[0].ID, group)
	}
	root := nextJSON[kindNextOut](t, env, fx.RepoDir, issue)
	if root.Kind != store.KindIssue || root.Chart != "plan" || root.Phase != nil || len(root.Children) != 4 {
		t.Errorf("jig next %s = %+v, want an issue of chart plan with 4 children and no phase", issue, root)
	}
	text := runJig(t, env, fx.RepoDir, "next", entries[0].ID).Stdout
	if strings.Contains(text, "prompt:") || !strings.Contains(text, "kind: multi-task") {
		t.Errorf("jig next %s:\n%s", entries[0].ID, text)
	}
	task := nextJSON[kindNextOut](t, env, fx.RepoDir, entries[1].ID)
	if task.Parent != entries[0].ID {
		t.Errorf("jig next %s parent = %q, want %s", entries[1].ID, task.Parent, entries[0].ID)
	}
	listing := nextJSON[struct {
		Next    []string `json:"next"`
		Tickets []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"tickets"`
	}](t, env, fx.RepoDir)
	for _, id := range listing.Next {
		if id == issue || id == entries[0].ID {
			t.Errorf("jig next lists %s as ready", id)
		}
	}

	// A multi-task under a multi-task is listed and refused, and mints nothing.
	issue2 := mint("issue")
	bad := filepath.Join(fx.StoreDir, "charts", "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "tickets.yaml"), []byte("issue: "+issue2+`
tickets:
  - title: Outer
    kind: multi-task
  - title: Inner
    kind: multi-task
    under: "#1"
  - title: Leaf
    under: "#2"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Tickets()
	v := runJig(t, env, fx.StoreDir, "validate", "bad")
	if v.Code == 0 || !strings.Contains(v.Stdout, `entry 2 ("Inner"): is a multi-task under entry 1; a multi-task holds tasks only`) {
		t.Errorf("jig validate bad: exit %d\n%s", v.Code, v.Stdout)
	}
	g := runJig(t, env, fx.StoreDir, "graduate", "bad")
	if g.Code == 0 || !strings.Contains(g.Stdout, "a multi-task holds tasks only") {
		t.Errorf("jig graduate bad: exit %d\n%s", g.Code, g.Stdout)
	}
	if after, _ := st.Tickets(); len(after) != len(before) {
		t.Errorf("graduate bad minted tickets: %d before, %d after", len(before), len(after))
	}
}
