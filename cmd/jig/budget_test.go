package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/spend"
)

func budgetFile(t *testing.T, storeDir, ticket string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeDir, "tickets", ticket, spend.BudgetFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestBudgetSetsAndShowsATicketBudget: the flags record budget.yaml in the
// ticket, a limit not named keeps its value, 0 takes a limit away, and
// without a flag the verb shows what is recorded.
func TestBudgetSetsAndShowsATicketBudget(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	T := fx.Ticket

	out, code := runMain(t, e, "", "budget", T)
	if code != 0 || !strings.Contains(out, "source: none\n") || !strings.Contains(out, "`jig budget "+T+" --tokens 5M --usd 20` sets one") {
		t.Fatalf("budget of a ticket with none: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "spend:") {
		t.Errorf("a ticket with no spend and no budget shows a spend block:\n%s", out)
	}

	out, code = runMain(t, e, "", "budget", T, "--tokens", "5M", "--usd", "$20")
	if code != 0 || !strings.Contains(out, "tokens: 5000000\n") || !strings.Contains(out, "usd: 20.00\n") || !strings.Contains(out, "source: ticket\n") {
		t.Fatalf("budget --tokens 5M --usd $20: exit %d\n%s", code, out)
	}
	if got := budgetFile(t, fx.StoreDir, T); got != "tokens: 5000000\nusd: 20\n" {
		t.Errorf("budget.yaml = %q", got)
	}

	if out, code = runMain(t, e, "", "budget", T, "--tokens", "6M"); code != 0 || !strings.Contains(out, "tokens: 6000000\n") || !strings.Contains(out, "usd: 20.00\n") {
		t.Errorf("budget --tokens 6M: exit %d, want the dollars kept\n%s", code, out)
	}
	if out, code = runMain(t, e, "", "budget", T, "--usd", "0"); code != 0 || !strings.Contains(out, "tokens: 6000000\n") || !strings.Contains(out, "usd: none\n") {
		t.Errorf("budget --usd 0: exit %d, want the dollar limit gone\n%s", code, out)
	}
	if out, code = runMain(t, e, "", "budget", T); code != 0 || !strings.Contains(out, "tokens: 6000000\n") || !strings.Contains(out, "source: ticket\n") {
		t.Errorf("budget (show): exit %d\n%s", code, out)
	}

	if out, code = runMain(t, e, "", "budget", T, "--tokens", "0"); code != 0 || !strings.Contains(out, "source: ticket\n") {
		t.Fatalf("budget --tokens 0: exit %d\n%s", code, out)
	}
	if out, _ = runMain(t, e, "", "budget", T); !strings.Contains(out, "source: ticket (none on purpose)\n") {
		t.Errorf("a ticket given no budget on purpose reads:\n%s", out)
	}
}

// TestBudgetStartsFromTheProjectDefault: the jig.yaml budget is what a ticket
// without a record starts from - shown as the default, recorded by --default,
// and the base of a flag that names only one limit.
func TestBudgetStartsFromTheProjectDefault(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	T := fx.Ticket

	if out, code := runMain(t, e, "", "budget", T, "--default"); code != 2 || !strings.Contains(out, "sets no default budget") {
		t.Fatalf("--default in a project with none: exit %d, want a refusal saying so\n%s", code, out)
	}

	m := project.Marker{Project: "fixture", Budget: &project.Budget{Tokens: 2_000_000, USD: 8}}
	if err := project.WriteMarker(fx.RepoDir, m); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, e, "", "budget", T)
	if code != 0 || !strings.Contains(out, "tokens: 2000000\n") || !strings.Contains(out, "usd: 8.00\n") ||
		!strings.Contains(out, "source: project default (not yet recorded on the ticket)\n") || !strings.Contains(out, "`jig budget "+T+" --default` records it") {
		t.Fatalf("budget under a project default: exit %d\n%s", code, out)
	}

	// Naming one limit starts the other from the default.
	if out, code = runMain(t, e, "", "budget", T, "--usd", "3"); code != 0 || !strings.Contains(out, "tokens: 2000000\n") || !strings.Contains(out, "usd: 3.00\n") || !strings.Contains(out, "source: ticket\n") {
		t.Fatalf("budget --usd 3: exit %d, want tokens from the default\n%s", code, out)
	}
	// --default puts it back; a flag with it wins.
	if out, code = runMain(t, e, "", "budget", T, "--default"); code != 0 || !strings.Contains(out, "usd: 8.00\n") {
		t.Fatalf("budget --default: exit %d\n%s", code, out)
	}
	if out, code = runMain(t, e, "", "budget", T, "--default", "--tokens", "1M"); code != 0 || !strings.Contains(out, "tokens: 1000000\n") || !strings.Contains(out, "usd: 8.00\n") {
		t.Fatalf("budget --default --tokens 1M: exit %d\n%s", code, out)
	}
}

// TestBudgetRefusals: what is not a count, an amount or a ticket is refused
// before anything is written.
func TestBudgetRefusals(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	T := fx.Ticket
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"budget", T, "--tokens", "lots"}, 2, `--tokens: "lots" is not a token count`},
		{[]string{"budget", T, "--usd", "-3"}, 2, `--usd: "-3" is not a dollar amount`},
		{[]string{"budget", "JIG-77", "--tokens", "1"}, 2, "JIG-77"},
		{[]string{"budget", T, "extra"}, 2, "at most one ticket"},
	} {
		out, code := runMain(t, e, "", c.args...)
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("jig %s: exit %d, want %d naming %q\n%s", strings.Join(c.args, " "), code, c.code, c.want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", T, spend.BudgetFile)); !os.IsNotExist(err) {
		t.Errorf("a refused budget left a record behind: %v", err)
	}
}

// TestStatusShowsSpendAgainstTheBudget: the spend block of status appears with
// the first spend line or budget, sums the latest line of each session, and
// says where the ticket stands.
func TestStatusShowsSpendAgainstTheBudget(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket

	plain, code := runMain(t, e, "", "status", T)
	if code != 0 || strings.Contains(plain, "spend:") {
		t.Fatalf("status of a ticket nobody measured: exit %d, want no spend block\n%s", code, plain)
	}

	for _, l := range []journal.Line{
		spend.Line("2026-01-01T10:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 100, Output: 50, CacheRead: 1000, CacheWrite: 200, USD: 1.5, Models: []string{"m1"}}),
		spend.Line("2026-01-01T11:00:00Z", "s-1", "claude", "a", spend.Totals{Input: 300, Output: 100, CacheRead: 2000, CacheWrite: 400, USD: 2.25, Models: []string{"m1"}}),
		spend.Line("2026-01-01T11:30:00Z", "s-2", "claude", "b", spend.Totals{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, USD: 0.25, Models: []string{"m2"}, Unpriced: []string{"m2"}}),
	} {
		if err := journal.Append(st, T, l); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runMain(t, e, "", "status", T)
	want := "spend:\n  sessions: 2\n  tokens: 2900\n  input: 310\n  output: 120\n  cache_write: 440\n  cache_read: 2030\n  usd: 2.50\n  budget: none\n  unpriced: m2\n"
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("status: exit %d, want it to hold the block\n%s\ngot:\n%s", code, want, out)
	}
	if i, j := strings.Index(out, "spend:"), strings.Index(out, "help["); i < 0 || j < i {
		t.Errorf("the spend block is not before the help block:\n%s", out)
	}

	if _, code := runMain(t, e, "", "budget", T, "--tokens", "3000"); code != 0 {
		t.Fatalf("budget: exit %d", code)
	}
	if out, _ = runMain(t, e, "", "status", T); !strings.Contains(out, "  budget_tokens: 3000\n  budget: near\n") {
		t.Errorf("status at 2900 of 3000:\n%s", out)
	}
	if _, code := runMain(t, e, "", "budget", T, "--tokens", "2000", "--usd", "2"); code != 0 {
		t.Fatalf("budget: exit %d", code)
	}
	if out, _ = runMain(t, e, "", "status", T); !strings.Contains(out, "  budget_tokens: 2000\n  budget_usd: 2.00\n  budget: over\n") {
		t.Errorf("status at 2900 of 2000:\n%s", out)
	}
	if out, _ = runMain(t, e, "", "budget", T); !strings.Contains(out, "spend:\n  sessions: 2\n") || !strings.Contains(out, "budget: over\n") {
		t.Errorf("budget (show) lacks the spend block:\n%s", out)
	}
}
