package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// needRun runs jig chart new in process on clone and returns exit and output.
func needRun(t *testing.T, e env, clone string, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	code := run(e, append([]string{"chart", "new", "--store", clone}, args...), &buf, strings.NewReader(""))
	return code, buf.String()
}

// TestChartNewNeedMakesTheIssueAndItsChart: --need mints an issue (kind issue,
// the chart's title, the need as its body) and the chart that records it, in
// two commits on the origin, prints both ids, and the issue validates.
func TestChartNewNeedMakesTheIssueAndItsChart(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginClone(t, clone)

	code, out := needRun(t, e, clone, "coupons", "--title", "Coupons at checkout", "--need", "  Shoppers want to enter a coupon.  ")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.ReadTicket("DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.KindOrDefault() != store.KindIssue || rec.Title != "Coupons at checkout" || rec.Body != "Shoppers want to enter a coupon." || rec.Slug != "coupons-at-checkout" {
		t.Errorf("issue = %+v, want an issue titled as the chart with the need as body", rec)
	}
	if got, _ := st.ChartIssue("coupons"); got != "DEMO-1" {
		t.Errorf("chart issue = %q, want DEMO-1", got)
	}
	for _, want := range []string{"issue: DEMO-1", "name: coupons", filepath.Join(st.Root, "charts", "coupons")} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	mapMD, err := os.ReadFile(st.ChartMapFile("coupons"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mapMD), "## Destination\n\nPlans issue DEMO-1.\n\n## Notes") || strings.Contains(string(mapMD), "coupon.") {
		t.Errorf("map Destination is not one line naming the issue:\n%s", mapMD)
	}
	if subject, _ := originHead(t, remote); subject != "chart coupons: new chart" {
		t.Errorf("origin head = %q", subject)
	}
	log, err := gitx.Run("", "--git-dir", remote, "log", "-3", "--pretty=%s")
	if err != nil || !strings.Contains(log, "DEMO-1: new ticket") {
		t.Errorf("origin log = %q, %v, want the issue's own commit", log, err)
	}
	if dirty, err := gitx.Run(clone, "status", "--porcelain"); err != nil || dirty != "" {
		t.Errorf("clone not clean: %q, %v", dirty, err)
	}
	vout, vcode := runMain(t, e, "", "validate", "DEMO-1", "--store", clone)
	if vcode != 0 {
		t.Errorf("jig validate DEMO-1: exit %d\n%s", vcode, vout)
	}
}

// TestChartNewNeedRefusalsMintNothing: a refused name, an empty need and
// --need with --issue each leave the store with no ticket and no chart.
func TestChartNewNeedRefusalsMintNothing(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"Bad Name", "--title", "T", "--need", "n"}, "not valid"},
		{[]string{"ok", "--title", "T", "--need", "   "}, "--need must not be empty"},
		{[]string{"ok", "--title", "T", "--need", ""}, "--need must not be empty"},
		{[]string{"ok", "--title", "T", "--need", "n", "--issue", "DEMO-1"}, "exclude each other"},
	} {
		code, out := needRun(t, e, clone, tc.args...)
		if code == 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%v: exit %d, want a refusal with %q:\n%s", tc.args, code, tc.want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(clone, "tickets")); err == nil {
		t.Errorf("a refusal minted a ticket")
	}
	if _, err := os.Stat(filepath.Join(clone, "charts")); err == nil {
		t.Errorf("a refusal made a chart")
	}

	// A name a chart holds, and a name a ticket holds, are refused before the
	// issue is minted: the store gains no ticket.
	if code, out := needRun(t, e, clone, "taken", "--title", "T"); code != 0 {
		t.Fatalf("jig chart new taken: exit %d\n%s", code, out)
	}
	if code, out := needRun(t, e, clone, "first", "--title", "T", "--need", "n"); code != 0 {
		t.Fatalf("jig chart new first --need: exit %d\n%s", code, out)
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	// A well-shaped name a ticket holds is one of its aliases.
	if err := st.CreateTicketRecord("DEMO-30", store.Ticket{Title: "Renamed", Aliases: []string{"oldname"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "DEMO-30: new ticket"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Tickets()
	for name, want := range map[string]string{
		"taken":   `chart "taken" already exists in the store at `,
		"oldname": `"oldname" is a ticket of the store`,
	} {
		if code, out := needRun(t, e, clone, name, "--title", "T", "--need", "n"); code == 0 || !strings.Contains(out, want) {
			t.Errorf("name %q: exit %d, want a refusal with %q\n%s", name, code, want, out)
		}
	}
	if after, _ := st.Tickets(); len(after) != len(before) {
		t.Errorf("a taken name minted an issue: tickets %v, were %v", after, before)
	}
}

// TestChartNewNeedChartFailureNamesTheRepair: when the chart's own commit
// fails after the issue landed, the command says the issue exists and names
// the --issue repair, and jig validate lists the issue as having no chart.
func TestChartNewNeedChartFailureNamesTheRepair(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)
	hook := "#!/bin/sh\nif git diff --cached --name-only | grep -q '^charts/'; then echo refused >&2; exit 1; fi\n"
	if err := os.MkdirAll(filepath.Join(clone, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := needRun(t, e, clone, "coupons", "--title", "Coupons", "--need", "Coupons please")
	if code == 0 {
		t.Fatalf("exit 0, want the chart write refused:\n%s", out)
	}
	for _, want := range []string{"issue DEMO-1 was made but its chart", "jig chart new coupons --title \"Coupons\" --issue DEMO-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	vout, vcode := runMain(t, e, "", "validate", "DEMO-1", "--store", clone)
	if vcode == 0 || !strings.Contains(vout, "issue has no chart") {
		t.Errorf("jig validate DEMO-1: exit %d, want 'issue has no chart':\n%s", vcode, vout)
	}
}

// TestChartNewNeedRunsTheCheckpointOnce: the issue and the chart are mirrored
// by one checkpoint, and the chart has no issue of its own, so the mirror opens
// one issue, the issue ticket's, not a second pass.
func TestChartNewNeedRunsTheCheckpointOnce(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	stub := &stubGitHub{}
	e.mirrorClient = stub

	code, out := needRun(t, e, clone, "coupons", "--title", "Coupons", "--need", "Coupons please")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if stub.created != 1 {
		t.Errorf("the mirror opened %d issues, want 1 (the issue ticket; its chart has no issue) from one checkpoint", stub.created)
	}
}
