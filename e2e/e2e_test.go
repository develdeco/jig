package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

// TestEndToEndTwice drives the full chain (run to first pause, answer
// to full green, a divergent store push that the next command must
// pull-rebase past, a gate round that finds a must-fix and appends a fix
// slice, clearing that fix slice, a clean gate, a target-branch move that
// publish must reconcile and revalidate against, and publish itself) twice
// end to end against two independent fixtures, then asserts the product
// repo's own working tree is left clean.
func TestEndToEndTwice(t *testing.T) {
	for i := 1; i <= 2; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			runEndToEndOnce(t)
		})
	}

	out, err := gitx.Run(repoRoot, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status --porcelain: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("product repo working tree is dirty after e2e runs:\n%s", out)
	}
}

// NOTE (known environment-triggered bug, not in this package's scope): on a
// machine where the Go toolchain lives under a path containing a space
// (e.g. the default Windows install at "C:\Program Files\Go"), every gate
// and publish oracle run fails with "... is not recognized as an internal
// or external command". fixture.rewriteJigYAML already quotes the
// substituted @GO path (quoteIfSpaced) for exactly this reason, but
// envrun.Shell's `exec.Command("cmd", "/C", cmd)` passes that pre-quoted
// string as a single Go argument; Go's own Windows argv-to-command-line
// escaping then backslash-escapes the embedded quotes, and cmd.exe's /C
// quote-stripping does not undo that escaping, corrupting the executable
// path it tries to resolve. Reproduced directly (outside this suite) with:
// cmd /C "\"C:/Program Files/Go/bin/go.exe\" version" -> same failure. This
// blocks any oracle run on such a machine, which in turn blocks
// TestEndToEndTwice's gate/publish steps and TestSolveOneProcess's chain.
// It is flagged as a follow-up task rather than fixed here since it lives
// in envrun (and/or fixture's quoting), neither of which is e2e's scope.
func runEndToEndOnce(t *testing.T) {
	fx, home := newFixture(t, fixture.Opts{})
	ticket := fx.Ticket
	repoName := (project.Repo{Remote: fx.RepoRemote}).Name()

	storeRevBaseline := gitLog(t, fx.StoreRemote, "rev-list", "--count", "main")

	// --- 1. run to first pause: a, b (retried), d green; c paused on q-001.
	r1 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	if !strings.Contains(r1.Stdout, "q-001") {
		t.Fatalf("run 1 stdout missing q-001:\n%s", r1.Stdout)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	assertSliceState(t, st, ticket, "a", "green", 1)
	assertSliceState(t, st, ticket, "b", "green", 2)
	assertSliceState(t, st, ticket, "c", "needs-input", 1)
	assertSliceState(t, st, ticket, "d", "green", 1)

	statusR1 := runJig(t, fx.StoreDir, "status", ticket)
	assertGolden(t, "status-run1-paused.txt", statusR1.Stdout)

	// --- 2. answer q-001: c goes green, ticket fully green.
	r2 := runJig(t, fx.StoreDir, "run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}
	assertSliceState(t, st, ticket, "c", "green", 2)

	// --- 3. scripted divergent store commit, then gate: pull-rebase + round 1.
	divergeMsg := "note: scripted divergent store commit"
	scriptedPush(t, fx.StoreRemote, "platform/note.md", "scripted divergence\n", divergeMsg)

	r3 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("gate (round 1) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}

	storeLog := gitLog(t, fx.StoreDir, "log", "--format=%s")
	if !strings.Contains(storeLog, divergeMsg) {
		t.Fatalf("store log after gate does not contain the scripted divergent commit %q:\n%s", divergeMsg, storeLog)
	}

	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	if !hasSlice(slices, "fix-1") {
		t.Fatalf("expected gate round 1 to append slice fix-1; got %+v", slices)
	}

	round1Findings := readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "gate", "round-1", "findings.md"))

	statusR3 := runJig(t, fx.StoreDir, "status", ticket)
	assertGolden(t, "status-gate-round1.txt", statusR3.Stdout)

	// --- 4. run clears fix-1; gate again is clean (round 2), round 1 untouched.
	r4 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r4.Code != 0 {
		t.Fatalf("run (fix-1) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r4.Code, r4.Stdout, r4.Stderr)
	}
	assertSliceState(t, st, ticket, "fix-1", "green", 1)

	r5 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if r5.Code != 0 {
		t.Fatalf("gate (round 2, clean) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r5.Code, r5.Stdout, r5.Stderr)
	}
	if _, err := os.Stat(joinPath(fx.StoreDir, ticket, "gate", "round-2")); err != nil {
		t.Fatalf("expected gate/round-2 to exist: %v", err)
	}
	round1FindingsAfter := readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "gate", "round-1", "findings.md"))
	if string(round1FindingsAfter) != string(round1Findings) {
		t.Fatalf("gate round-1 findings.md changed after round 2 ran; rounds must be immutable")
	}

	lines := readJournal(t, fx, ticket)
	if _, ok := findJournalLine(lines, "gate-clean", "", ""); !ok {
		t.Fatalf("no gate-clean journal line; journal:\n%+v", lines)
	}

	// --- capture the pre-squash slice-commit set from the build lease before
	// publish's reconcile/rebase can touch anything, using the recorded
	// fork-point sha (stable regardless of later fetches) rather than the
	// lease's own possibly-stale origin/<target> tracking ref.
	startSHA := strings.TrimSpace(string(readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "start."+repoName+".sha"))))
	buildLeaseDir := poolBuildLeaseDir(home, repoName, ticket)
	preSquashMessages := strings.Split(gitLog(t, buildLeaseDir, "log", "--format=%s", startSHA+"..HEAD"), "\n")
	wantMessages := expectedSliceCommitMessages(t, fx, ticket)
	assertSameMessageSet(t, preSquashMessages, wantMessages)

	// --- 5. scripted target move on the repo remote's main, then publish.
	scriptedPush(t, fx.RepoRemote, "NOTES.md", "upstream moved on\n", "upstream: unrelated change")
	movedTip := gitLog(t, fx.RepoRemote, "rev-parse", "main")

	r6 := runJig(t, fx.StoreDir, "publish", ticket, "--yes")
	if r6.Code != 0 {
		t.Fatalf("publish exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r6.Code, r6.Stdout, r6.Stderr)
	}

	lines = readJournal(t, fx, ticket)
	if _, ok := findJournalLine(lines, "revalidate", "", "oracles-only:target-moved"); !ok {
		t.Fatalf("no revalidate journal line with outcome oracles-only:target-moved; journal:\n%+v", lines)
	}

	branchRef := "refs/heads/jig/" + ticket
	if _, err := gitx.Run(fx.RepoRemote, "rev-parse", "--verify", branchRef); err != nil {
		t.Fatalf("published branch jig/%s missing on repo remote: %v", ticket, err)
	}
	beyond := gitLog(t, fx.RepoRemote, "rev-list", "--count", movedTip+"..jig/"+ticket)
	if beyond != "1" {
		t.Fatalf("commits beyond moved main tip on jig/%s = %s, want 1 (the squash)", ticket, beyond)
	}
	squashMsg := gitLog(t, fx.RepoRemote, "log", "-1", "--format=%s", "jig/"+ticket)
	if !strings.HasPrefix(squashMsg, ticket+": ") {
		t.Fatalf("squash commit message = %q, want prefix %q", squashMsg, ticket+": ")
	}
	retrieval, err := gitx.Run(fx.RepoRemote, "show", "jig/"+ticket+":.claude/retrieval/"+ticket+".md")
	if err != nil {
		t.Fatalf("squash commit tree missing .claude/retrieval/%s.md: %v", ticket, err)
	}
	if strings.TrimSpace(retrieval) == "" {
		t.Fatalf("memorize retrieval doc is empty")
	}
	mainTip := gitLog(t, fx.RepoRemote, "rev-parse", "main")
	if mainTip != movedTip {
		t.Fatalf("publish must never move the target branch: main = %s, want unchanged %s", mainTip, movedTip)
	}

	// --- changelogs, ledger, contract index, PR body, tracker projections.
	for _, ws := range []string{"alpha", "beta"} {
		data := readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "changelog", ws+".md"))
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("changelog for workspace %s is empty", ws)
		}
	}
	consolidated := readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "changelog", "consolidated.md"))
	if strings.TrimSpace(string(consolidated)) == "" {
		t.Fatalf("consolidated changelog is empty")
	}

	ledger := string(readFileOrFatal(t, joinPath(fx.StoreDir, "ledger.md")))
	if !strings.Contains(ledger, ticket) {
		t.Fatalf("ledger.md missing an entry for %s:\n%s", ticket, ledger)
	}
	if !strings.Contains(ledger, "Answers") {
		t.Fatalf("ledger.md missing an Answers section:\n%s", ledger)
	}
	if !strings.Contains(ledger, "q-001") || !strings.Contains(ledger, "Casual.") {
		t.Fatalf("ledger.md Answers tail missing q-001/Casual.:\n%s", ledger)
	}

	contractIndex := string(readFileOrFatal(t, joinPath(fx.StoreDir, "platform", "contract-index.md")))
	if !strings.Contains(contractIndex, ticket) {
		t.Fatalf("contract-index.md missing an entry for %s:\n%s", ticket, contractIndex)
	}
	if !strings.Contains(contractIndex, "alpha") || !strings.Contains(contractIndex, "beta") {
		t.Fatalf("contract-index.md entry missing workspace names:\n%s", contractIndex)
	}

	prBody := string(readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "pr", repoName+".md")))
	if strings.TrimSpace(prBody) == "" {
		t.Fatalf("PR body file is empty")
	}

	ticketMD := string(readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "tracker", "ticket.md")))
	if strings.TrimSpace(ticketMD) == "" {
		t.Fatalf("tracker/ticket.md is empty")
	}
	subtasksData := readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "tracker", "subtasks.yaml"))
	var subtasksFile struct {
		Subtasks []tracker.Subtask `yaml:"subtasks"`
	}
	if err := yaml.Unmarshal(subtasksData, &subtasksFile); err != nil {
		t.Fatalf("parse tracker/subtasks.yaml: %v", err)
	}
	if len(subtasksFile.Subtasks) != 5 {
		t.Fatalf("tracker subtasks count = %d, want 5 (a,b,c,d,fix-1); got %+v", len(subtasksFile.Subtasks), subtasksFile.Subtasks)
	}

	commentsDir := joinPath(fx.StoreDir, ticket, "tracker", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read tracker/comments: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected route to leave at least one tracker comment")
	}

	storeRevAfter := gitLog(t, fx.StoreRemote, "rev-list", "--count", "main")
	if storeRevAfter == storeRevBaseline {
		t.Fatalf("store remote did not advance: still %s revs on main", storeRevAfter)
	}
}

func assertSliceState(t *testing.T, st *store.Store, ticket, slice, wantState string, wantAttempts int) {
	t.Helper()
	s, err := st.ReadSliceState(ticket, slice)
	if err != nil {
		t.Fatalf("ReadSliceState(%s): %v", slice, err)
	}
	if s.State != wantState {
		t.Fatalf("slice %s state = %q, want %q", slice, s.State, wantState)
	}
	if s.Attempts != wantAttempts {
		t.Fatalf("slice %s attempts = %d, want %d", slice, s.Attempts, wantAttempts)
	}
}

func hasSlice(slices []store.Slice, id string) bool {
	for _, s := range slices {
		if s.ID == id {
			return true
		}
	}
	return false
}
