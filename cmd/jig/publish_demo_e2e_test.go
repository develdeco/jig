package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestPublishAttachesTheGateDemoThroughMain drives a clean gate round with
// its demo, then `jig publish`, through Main on the local tracker - the half
// of the demo contract ADR 0014 leaves to publish: the shipped head's
// recorded demo lands in the pull request body as a `## Demo` section
// between `## What changed` and `## Verification`, referencing each
// verified file (both are svg here, so each as a markdown image reference)
// with its caption, right after the summary.
// demo/publish-body.tape's own hidden setup already reaches this
// state (the `reviewer` fixture scenario scripts a demo for its clean round
// 3) but never shows the body on camera with a demo in it; this test is
// what a tape showing one would cite.
func TestPublishAttachesTheGateDemoThroughMain(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "demo"})
	e := testEnv(fx.Home)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)

	gate := []string{"gate", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, e, "", gate...)
	if code != 0 || !strings.Contains(out, "verdict: clean") || !strings.Contains(out, "demo: recorded") {
		t.Fatalf("gate: exit = %d, want 0 with a recorded demo\n%s", code, out)
	}

	out, code = runMain(t, e, "", "publish", fx.Ticket, "--yes", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish: exit = %d, want 0\n%s", code, out)
	}

	body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil {
		t.Fatalf("read pr/fixture-repo.md: %v", err)
	}
	text := string(body)

	// The section sits between What changed and Verification, in that order.
	whatChanged := strings.Index(text, "## What changed")
	demo := strings.Index(text, "## Demo")
	verification := strings.Index(text, "## Verification")
	if whatChanged == -1 || demo == -1 || verification == -1 || !(whatChanged < demo && demo < verification) {
		t.Fatalf("pr body sections out of order (What changed=%d, Demo=%d, Verification=%d):\n%s", whatChanged, demo, verification, text)
	}

	for _, want := range []string{
		"two frames drawn from the fixture test cases for the clamp bound and the greeting",
		"- ![Clamp now holds the upper bound at 10](./demo-1.svg)",
		"- ![Greet reads casual](./demo-2.svg)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pr body missing %q:\n%s", want, text)
		}
	}
}
