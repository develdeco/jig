package e2e

// End-to-end test of a chart's spend: the spend lines the hooks write on the
// tickets of a chart, a subagent's on its own ticket (TestSubagentSpendIsItsOwnTickets
// has the chain), are summed by `jig next <chart>`, `jig status <chart>` and the
// JSON the chart screen reads.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// chartSpendOut is the part of `jig next <chart> --json` this test reads: the
// spend object, which is a pointer so that a standing with none reads as nil.
type chartSpendOut struct {
	Chart string `json:"chart"`
	Spend *struct {
		Tickets    int      `json:"tickets"`
		Agents     int      `json:"agents"`
		Tokens     int64    `json:"tokens"`
		Input      int64    `json:"input"`
		Output     int64    `json:"output"`
		CacheRead  int64    `json:"cache_read"`
		CacheWrite int64    `json:"cache_write"`
		USD        float64  `json:"usd"`
		Unpriced   []string `json:"unpriced"`
	} `json:"spend"`
}

// spendLineRE is the spend line of the chart's text: the tokens, the dollars of
// a priced model (the fake transcript's is, at list price: under half a cent
// here, which reads "<$0.01" and never "$0.00"), the tickets and the subagents.
var spendLineRE = regexp.MustCompile(`(?m)^\s*spend: 1\.3k tokens \(<\$0\.01\) over 2 tickets with 1 subagent$`)

// TestChartSpendIsTheSumOfItsTickets: a chart of two graduated tickets has no
// spend until a session writes some. The main session works on the first ticket
// and a subagent of it in a linked worktree on the second; after the main Stop,
// the subagent's stop and the main Stop again, the chart's spend is the main
// agent's tokens (on the first ticket, without the subagent's) plus the
// subagent's (on the second), as the JSON, `jig next <chart>` and `jig status
// <chart>` say it.
func TestChartSpendIsTheSumOfItsTickets(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	chartEnv := env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "map.md"), []byte(
		"# Coupons at checkout\n\n## Destination\n\nA coupon takes money off an order.\n"))
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte("tickets:\n  - title: Apply a coupon code\n  - title: Total with coupons\n"))
	if r := runJig(t, chartEnv, fx.StoreDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\n%s", r.Code, r.Stdout)
	}
	entries, err := st.ReadChart("c")
	if err != nil || len(entries) != 2 || entries[0].ID == "" || entries[1].ID == "" {
		t.Fatalf("the graduated chart has entries %+v (%v), want two with ids", entries, err)
	}
	first, second := entries[0].ID, entries[1].ID

	// Before any hook there is no spend, in the JSON and in the text.
	if c := nextJSON[chartSpendOut](t, chartEnv, fx.RepoDir, "c"); c.Spend != nil {
		t.Errorf("spend before any hook = %+v, want none", c.Spend)
	}
	raw := runJig(t, chartEnv, fx.RepoDir, "next", "c", "--json")
	if strings.Contains(raw.Stdout, `"spend"`) {
		t.Errorf("jig next c --json has a spend key before any hook:\n%s", raw.Stdout)
	}
	for _, verb := range []string{"next", "status"} {
		if r := runJig(t, chartEnv, fx.RepoDir, verb, "c"); r.Code != 0 || strings.Contains(r.Stdout, "spend:") {
			t.Errorf("jig %s c before any hook: exit %d, want 0 and no spend line:\n%s", verb, r.Code, r.Stdout)
		}
	}

	// The main session on the first ticket's branch, a subagent in a worktree on
	// the second's.
	main, sub := newAgentPair(t, fx, env, "s-run", "a-1", first+"-first", second+"-second")
	addUsage(t, main.Transcript, "s-run", "m-main-1", 1000)
	addAgentUsage(t, sub, "a-1", "m-agent-1", 300)
	if r := main.Hook("SessionStart", map[string]any{"source": "startup"}); r.Code != 0 {
		t.Fatalf("SessionStart: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := main.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("Stop: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := sub.Hook("SubagentStop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("SubagentStop: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := main.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("second Stop: exit %d\n%s", r.Code, r.Stderr)
	}
	// The lines are where the chain of the previous test puts them: the main
	// agent's 1000 on the first ticket, the subagent's 300 on the second.
	if l := spendLinesOn(t, fx, first); len(l) != 2 || l[1].Agent != "" || l[1].Spend == nil || l[1].Spend.Input != 1000 {
		t.Fatalf("spend lines of %s = %+v, want two, the second the session's at 1000", first, l)
	}
	if l := spendLinesOn(t, fx, second); len(l) != 1 || l[0].Agent != "a-1" || l[0].Spend == nil || l[0].Spend.Input != 300 {
		t.Fatalf("spend lines of %s = %+v, want one, the agent's at 300", second, l)
	}

	c := nextJSON[chartSpendOut](t, chartEnv, fx.RepoDir, "c")
	if c.Spend == nil {
		t.Fatal("jig next c --json has no spend after the hooks")
	}
	if s := c.Spend; s.Tickets != 2 || s.Agents != 1 || s.Tokens != 1000+300 || s.Input != 1300 || s.Output != 0 || s.CacheRead != 0 || s.CacheWrite != 0 || s.USD <= 0 || len(s.Unpriced) != 0 {
		t.Errorf("spend = %+v, want 2 tickets, 1 agent, 1300 tokens (the main's 1000 plus the agent's 300), a price and no unpriced model", *s)
	}
	for _, verb := range []string{"next", "status"} {
		r := runJig(t, chartEnv, fx.RepoDir, verb, "c")
		if r.Code != 0 || !spendLineRE.MatchString(r.Stdout) {
			t.Errorf("jig %s c: exit %d, want a line matching %s:\n%s", verb, r.Code, spendLineRE, r.Stdout)
		}
	}
}
