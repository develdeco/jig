package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
)

// proseOracle is the shape two reviewers wrote: a command with an explanation
// in parentheses, which no sh can parse.
const proseOracle = "go test -count=1 ./alpha -run TestAdd (extended to pin the overflow sentence: a, b) && go vet ./alpha"

// runnableOracle is a compound oracle with quotes, a bang, a pipe and a
// trailing comment, all of which sh parses.
const runnableOracle = `go test -count=1 ./alpha -run 'TestAdd|TestSub' && ! grep -rn "TODO" alpha | wc -l # nothing left`

func findingArgs(ticket, store, oracle string) []string {
	return []string{"finding", ticket, "--file", "alpha/alpha.go", "--line", "5", "--title", "Add overflows",
		"--detail", "Say what happens past the int range.", "--action", "fix", "--risk", "low", "--risk-rationale", "r",
		"--oracle", oracle, "--store", store}
}

// TestFindingRefusesAnOracleShCannotParse: `jig finding --oracle` with words
// sh cannot parse is refused with ORACLE_NOT_RUNNABLE, sh's message and a help
// line naming --detail, and no draft is stored; the same finding with the
// command alone is stored.
func TestFindingRefusesAnOracleShCannotParse(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}

	out, code := runMain(t, e, "", findingArgs(ticket, fx.StoreDir, proseOracle)...)
	if code != 1 || !strings.Contains(out, "ORACLE_NOT_RUNNABLE") {
		t.Fatalf("jig finding with a prose oracle: exit = %d, want 1 and ORACLE_NOT_RUNNABLE\n%s", code, out)
	}
	// sh's own words differ by sh (bash and dash), so the test asks the same sh.
	var se *envrun.SyntaxError
	if err := envrun.CheckSyntax(proseOracle); !errors.As(err, &se) {
		t.Fatalf("CheckSyntax(proseOracle) = %v, want a *SyntaxError", err)
	}
	for _, want := range []string{"--oracle is not a command sh can run: " + se.Msg, "--detail", "command alone"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}

	out, code = runMain(t, e, "", findingArgs(ticket, fx.StoreDir, "go test -count=1 ./alpha -run TestAdd")...)
	if code != 0 || !strings.Contains(out, "drafts: 1") {
		t.Fatalf("jig finding with the command alone: exit = %d, want 0 and drafts: 1 (the refused one stored nothing)\n%s", code, out)
	}
}

// TestFindingStoresTheOracleWhenNoShIsFound: with no sh to ask, the finding is
// stored as given, since the run of any oracle would fail the same way.
func TestFindingStoresTheOracleWhenNoShIsFound(t *testing.T) {
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	t.Setenv("PATH", "")
	out, code := runMain(t, e, "", findingArgs(ticket, fx.StoreDir, proseOracle)...)
	if code != 0 || !strings.Contains(out, "drafts: 1") {
		t.Fatalf("jig finding with no sh: exit = %d, want 0 and drafts: 1\n%s", code, out)
	}
}

// TestFindingStoresARunnableOracle: a compound oracle sh can parse reaches the
// round's report byte for byte.
func TestFindingStoresARunnableOracle(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", "review", "context", ticket, "--json", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, ctx)
	}
	out, code := runMain(t, e, "", findingArgs(ticket, fx.StoreDir, runnableOracle)...)
	if code != 0 || !strings.Contains(out, "drafts: 1") {
		t.Fatalf("jig finding with a runnable oracle: exit = %d, want 0 and drafts: 1\n%s", code, out)
	}
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json: %v\n%s", err, ctx)
	}
	if out, code := runMain(t, e, "", "review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "one fix", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig review done: exit = %d\n%s", code, out)
	}
	var found bool
	err := filepath.WalkDir(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, rerr := os.ReadFile(p)
			if rerr == nil && strings.Contains(string(b), runnableOracle) {
				found = true
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Errorf("no file of the round's report carries the oracle %q as written", runnableOracle)
	}
}
