package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
)

// commitInBuildLease writes files (repo-relative path to content) in the
// build lease, commits them on the ticket branch and pushes it, and returns
// the new commit.
func commitInBuildLease(t *testing.T, fx *fixture.Fixture, files map[string]string) string {
	t.Helper()
	dir := buildLeaseDir(t, fx)
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builder(t).Commit(dir, "a change after the build")
	builder(t).Push(dir, "origin", ticketBranch(fx.Ticket))
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// testCmd is the fixture repo's test oracle command in workspace ws, as the
// manifest at the build lease's head resolves it: the command a builder's
// `jig verify` records on its oracle line.
func testCmd(t *testing.T, fx *fixture.Fixture, ws string) string {
	t.Helper()
	man, err := manifest.Resolve(buildLeaseDir(t, fx))
	if err != nil {
		t.Fatal(err)
	}
	w, ok := man.Workspace(ws)
	if !ok {
		t.Fatalf("no workspace %s", ws)
	}
	return man.OracleCmd("test", w)
}

// recordPass journals an oracle line the way `jig verify` writes one for a
// slice: a pass of command at commit, in seconds, with the reason it covers
// nothing when uncovered is not empty.
func recordPass(t *testing.T, d Deps, ticket, slice, commit, command string, seconds int, uncovered string) {
	t.Helper()
	l := journal.Line{Slice: slice, Event: journal.EventOracle, Outcome: "pass", Commit: commit, Attempt: 1, Command: command, Seconds: seconds, Uncovered: uncovered, Source: "jig"}
	if err := journal.Append(d.Store, ticket, l); err != nil {
		t.Fatal(err)
	}
}

// builtCommit is the commit the journal says slice was verified at.
func builtCommit(t *testing.T, d Deps, ticket, slice string) string {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var sha string
	for _, l := range lines {
		if l.Slice == slice && l.Event == journal.EventVerified {
			sha = l.Commit
		}
	}
	if sha == "" {
		t.Fatalf("test setup: the journal has no verified line for slice %s", slice)
	}
	return sha
}

// passRows maps each oracles_passed row to its slice.
func passRows(passes []SlicePass) map[string]SlicePass {
	got := map[string]SlicePass{}
	for _, p := range passes {
		got[p.Slice] = p
	}
	return got
}

// TestOraclesPassedAreTheSlicesWitnessedPasses: review.json's oracles_passed
// lists, for each slice of the ticket, its latest covering pass of its own
// command at a commit in the head's history, in slice order, with the
// workspace, command, commit and seconds the builder's run recorded, and no
// row for a slice that has none.
func TestOraclesPassedAreTheSlicesWitnessedPasses(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha, beta := testCmd(t, fx, "alpha"), testCmd(t, fx, "beta")
	a, b, c := builtCommit(t, d, fx.Ticket, "a"), builtCommit(t, d, fx.Ticket, "b"), builtCommit(t, d, fx.Ticket, "c")
	recordPass(t, d, fx.Ticket, "a", a, alpha, 3, "")
	recordPass(t, d, fx.Ticket, "b", b, alpha, 5, "")
	recordPass(t, d, fx.Ticket, "c", c, beta, 7, "")
	// Slice d has no pass, and a later pass of slice a stands in for its first.
	recordPass(t, d, fx.Ticket, "a", a, alpha, 4, "")

	got := gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed
	var order []string
	for _, p := range got {
		order = append(order, p.Slice)
	}
	if strings.Join(order, ",") != "a,b,c" {
		t.Fatalf("oracles_passed rows = %v, want slices a, b and c in slice order: d has no pass", order)
	}
	rows := passRows(got)
	want := map[string]SlicePass{
		"a": {Slice: "a", Workspace: "alpha", Command: alpha, Commit: a, Seconds: 4},
		"b": {Slice: "b", Workspace: "alpha", Command: alpha, Commit: b, Seconds: 5},
		"c": {Slice: "c", Workspace: "beta", Command: beta, Commit: c, Seconds: 7},
	}
	for id, w := range want {
		g := rows[id]
		g.AtHead = ""
		if g != w {
			t.Errorf("row of slice %s = %+v, want %+v", id, rows[id], w)
		}
	}
}

// TestAPassOffTheHeadsHistoryIsNotListed: a pass on a commit the head does not
// descend from stands for nothing, whoever recorded it.
func TestAPassOffTheHeadsHistoryIsNotListed(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha := testCmd(t, fx, "alpha")
	elsewhere := authorBranch(t, fx, "somewhere-else")
	recordPass(t, d, fx.Ticket, "a", elsewhere, alpha, 3, "")
	recordPass(t, d, fx.Ticket, "b", builtCommit(t, d, fx.Ticket, "b"), alpha, 3, "")

	rows := passRows(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
	if _, ok := rows["a"]; ok {
		t.Errorf("rows = %+v, want none for slice a: its pass is on a commit outside the head's history", rows)
	}
	if _, ok := rows["b"]; !ok {
		t.Errorf("rows = %+v, want slice b's pass, which is in the history", rows)
	}
}

// TestAnUncoveredRunIsNotListed: a run that covers nothing (a dirty tree, a
// moved head) and a failed run are no witnessed pass; the latest covering one
// stays the row.
func TestAnUncoveredRunIsNotListed(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha := testCmd(t, fx, "alpha")
	a, b := builtCommit(t, d, fx.Ticket, "a"), builtCommit(t, d, fx.Ticket, "b")
	recordPass(t, d, fx.Ticket, "a", a, alpha, 3, "tree dirty after")
	recordPass(t, d, fx.Ticket, "b", b, alpha, 3, "")
	recordPass(t, d, fx.Ticket, "b", b, alpha, 9, "head moved")
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Slice: "b", Event: journal.EventOracle, Outcome: "fail", Commit: b, Attempt: 2, Command: alpha, Seconds: 2}); err != nil {
		t.Fatal(err)
	}

	rows := passRows(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
	if _, ok := rows["a"]; ok {
		t.Errorf("rows = %+v, want none for slice a: its only run covers nothing", rows)
	}
	if rows["b"].Seconds != 3 {
		t.Errorf("slice b's row = %+v, want its covering pass (3 s), not the uncovered or the failed run", rows["b"])
	}
}

// TestOraclesPassedSaysWhetherThePassWasAtTheHeadsTree: at_head is yes for a
// pass at a commit with the head's tree and no for one the builder made on
// earlier code, and a delta round lists the table at its new head.
func TestOraclesPassedSaysWhetherThePassWasAtTheHeadsTree(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha := testCmd(t, fx, "alpha")
	a := builtCommit(t, d, fx.Ticket, "a")
	head := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "the code at the head\n"})
	recordPass(t, d, fx.Ticket, "a", a, alpha, 3, "")
	recordPass(t, d, fx.Ticket, "b", head, alpha, 3, "")

	rows := passRows(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
	if rows["a"].AtHead != "no" || rows["b"].AtHead != "yes" || rows["b"].Commit != head {
		t.Errorf("rows = %+v, want slice a's earlier pass at_head no and slice b's pass at the head yes", rows)
	}
}

// failingOracleManifest makes the fixture's test oracle a command that writes
// marker and fails, on the ticket's branch, so a gate that ran it would leave
// the marker and refuse.
func failingOracleManifest(t *testing.T, fx *fixture.Fixture, marker string) {
	t.Helper()
	buildDir := buildLeaseDir(t, fx)
	yamlPath := filepath.Join(buildDir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	oracles := doc["oracles"].(map[string]any)
	oracles["test"] = "echo ran > '" + filepath.ToSlash(marker) + "'; exit 1"
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	builder(t).Commit(buildDir, "an oracle that fails")
	builder(t).Push(buildDir, "origin", ticketBranch(fx.Ticket))
}

// TestAGateRoundRunsNoCommand: a manifest whose oracle fails and whose slice
// names an env class still opens the round: no command ran, no env class came
// up, no suite slot was taken and no gate-oracle line was written.
func TestAGateRoundRunsNoCommand(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	marker := filepath.Join(t.TempDir(), "ran")
	failingOracleManifest(t, fx, marker)

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v, want the round opened whatever the oracles would do", err)
	}
	if p.Round != 1 {
		t.Errorf("Round = %d, want 1", p.Round)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("the failing oracle ran: %s exists", marker)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		switch l.Event {
		case "gate-oracle", journal.EventEnvUp, journal.EventEnvDown, journal.EventEnvUnavailable, journal.EventSlotWait, journal.EventSlotTaken:
			t.Errorf("the gate round wrote a %s line: %+v", l.Event, l)
		}
	}
}

// TestAGateRoundAtAStandingHeadIsStillReported: a second call at a head the
// round already stands on re-opens the same round and reports it as before.
func TestAGateRoundAtAStandingHeadIsStillReported(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)

	first, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	second, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare again: %v", err)
	}
	if first.Round != 1 || second.Round != first.Round || second.Head != first.Head {
		t.Errorf("rounds = %d at %v, then %d at %v, want the same open round at the same head", first.Round, first.Head, second.Round, second.Head)
	}
}
