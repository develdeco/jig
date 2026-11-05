package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestChartVerdictIntent runs the built binary and reads what a session reads
// at a chart verdict: the three outcomes in intake, the chart skill's three
// prompts and its skipped allow question, the here skill's pointer, and the
// stub jig attach writes. The text stays within the skill budget.
func TestChartVerdictIntent(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	skill := func(name string) string {
		t.Helper()
		r := runJig(t, env, fx.RepoDir, "skill", name)
		if r.Code != 0 {
			t.Fatalf("jig skill %s exit = %d\nstdout:\n%s\nstderr:\n%s", name, r.Code, r.Stdout, r.Stderr)
		}
		return r.Stdout
	}
	intake, chart, here := skill("intake"), skill("chart"), skill("here")

	for _, want := range []string{
		"**No intent**", "**Intent, not fully explicit**", "**Fully explicit**",
		"\"Take <ticket> to PR\"", "\"Take <chart> to PR\"", "ask nothing", "\"not now\" the other answer", "unattended, stop",
		"Why: a ticket's terms ask for a ticket run, which a chart verdict ends", "unsure, ask",
		"run `jig skill chart`", "its own `id`, `title` and `body` scoped to its part", "graduate keeps it `existing`",
	} {
		if !strings.Contains(intake, want) {
			t.Errorf("jig skill intake lacks %q", want)
		}
	}
	for _, want := range []string{
		"Three prompts make a chart run", "a yes to intake's confirm question", "intake's explicit launch",
		"A run going with the recommendations, intake's explicit launch included, asks nothing",
		"permission settings decide", "a refused command hands its ticket back", "the report names it",
	} {
		if !strings.Contains(chart, want) {
			t.Errorf("jig skill chart lacks %q", want)
		}
	}
	if want := "what follows depends on the prompt's intent to launch: `jig skill intake`, \"Then\""; !strings.Contains(here, want) {
		t.Errorf("jig skill here lacks %q", want)
	}
	for name, text := range map[string]string{"intake": intake, "chart": chart, "here": here} {
		if strings.Contains(text, "ask whether to take") {
			t.Errorf("jig skill %s still asks whether to take the chart's tickets on", name)
		}
		if n := strings.Count(text, "\n"); n > 100 {
			t.Errorf("jig skill %s is %d lines, want at most 100", name, n)
		}
	}

	// The stub jig attach writes carries the same rule, not the old question.
	r := newHookRepo(t)
	r.markProject("project: demo\nkind: project\n")
	if res := r.jigIn(r.clone, "attach", "--yes"); res.Code != 0 {
		t.Fatalf("jig attach exit = %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	stub, err := os.ReadFile(filepath.Join(r.clone, ".claude", "skills", "jig", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"What follows a chart verdict depends on the prompt's intent to launch", "a fully explicit one starts the chart run", "read `jig skill intake`"} {
		if !bytes.Contains(stub, []byte(want)) {
			t.Errorf("the stub lacks %q:\n%s", want, stub)
		}
	}
	if bytes.Contains(stub, []byte("ask whether to take its tickets on")) {
		t.Errorf("the stub still asks whether to take the tickets on:\n%s", stub)
	}
	if n := bytes.Count(stub, []byte("\n")); n > 100 {
		t.Errorf("the stub is %d lines, want at most 100", n)
	}
}

// TestChartVerdictNamedTicket: a chart entry that carries an existing
// ticket's id is kept by jig graduate (reported existing), the entry without
// one is created, and the existing ticket's folder is not touched.
func TestChartVerdictNamedTicket(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, _ = ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	mint := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Apply a coupon code")
	m := regexp.MustCompile(`(?m)^\s*id: (\S+)`).FindStringSubmatch(mint.Stdout)
	if mint.Code != 0 || m == nil {
		t.Fatalf("jig ticket new: exit %d\n%s%s", mint.Code, mint.Stdout, mint.Stderr)
	}
	id := m[1]
	if r := runJig(t, env, fx.StoreDir, "chart", "new", "coupons", "--title", "Coupons at checkout"); r.Code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	writeFileOrFatal(t, st.ChartFile("coupons"), []byte("tickets:\n  - id: "+id+"\n    title: Apply a coupon code\n    body: Only the apply step of the coupon work.\n  - title: Receipt shows the coupon\n"))

	ticketFile := filepath.Join(st.TicketDir(id), "ticket.yaml")
	before, err := os.ReadFile(ticketFile)
	if err != nil {
		t.Fatal(err)
	}
	r := runJig(t, env, fx.StoreDir, "graduate", "coupons")
	if r.Code != 0 {
		t.Fatalf("jig graduate coupons: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	out := strings.ReplaceAll(r.Stdout, "\r\n", "\n")
	if !strings.Contains(out, ",existing") || !strings.Contains(out, "Apply a coupon code,existing") || !strings.Contains(out, "Receipt shows the coupon,created") {
		t.Errorf("jig graduate coupons did not report the named ticket existing and the other created:\n%s", out)
	}
	after, err := os.ReadFile(ticketFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("graduate changed %s:\nbefore:\n%s\nafter:\n%s", id, before, after)
	}
}
