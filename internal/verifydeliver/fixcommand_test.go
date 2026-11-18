package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
)

// fixFinding is an open fix finding in workspace ws that names command.
func fixFinding(id, file, ws, command string) Finding {
	return Finding{ID: id, File: file, Title: "t " + id, RiskRationale: "r", Action: ActionFix, Oracle: command, Workspace: ws, Status: StatusOpen}
}

// TestAFixSliceTakesItsFindingsCommands: a fix slice's oracle is the command
// its finding named, verbatim, whatever the manifest declares.
func TestAFixSliceTakesItsFindingsCommands(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	const command = "go test ./internal/x -run 'TestA|TestB' && ! git ls-files --error-unmatch cmd/x.exe"
	_, slices, err := routeRound(1, st, "T-1", nil, []Finding{fixFinding("r1-f1", "a.go", "root", command)}, nil, nil, oneRepoFiles(oneOracleManifest()), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 || slices[0].Oracle != command {
		t.Fatalf("slices = %+v, want one slice whose oracle is the finding's command verbatim", slices)
	}
}

// TestAFixSliceJoinsTheCommandsOfItsFindingsOnce: several findings of a slice
// give an oracle of their commands in finding-id order, each in parentheses,
// identical text once, and a command holding a # on its own lines.
func TestAFixSliceJoinsTheCommandsOfItsFindingsOnce(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	reported := []Finding{
		fixFinding("r1-f3", "c.go", "root", "go test ./c -run 'TestC'"),
		fixFinding("r1-f1", "z.go", "root", "go test ./a -run 'TestA' # the a test"),
		fixFinding("r1-f2", "b.go", "root", "go test ./c -run 'TestC'"),
		fixFinding("r1-f4", "d.go", "root", "go vet ./d"),
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(oneOracleManifest()), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("slices = %v, want one", sliceIDs(slices))
	}
	want := "(\ngo test ./a -run 'TestA' # the a test\n) && (go test ./c -run 'TestC') && (go vet ./d)"
	if slices[0].Oracle != want {
		t.Errorf("oracle = %q, want %q", slices[0].Oracle, want)
	}

	// Identical commands alone collapse to the one, verbatim.
	same := []Finding{fixFinding("r1-f1", "a.go", "root", "go vet ./a"), fixFinding("r1-f2", "b.go", "root", "go vet ./a")}
	_, slices, err = routeRound(1, st, "T-1", nil, same, nil, nil, oneRepoFiles(oneOracleManifest()), 0, 0, 5)
	if err != nil || len(slices) != 1 || slices[0].Oracle != "go vet ./a" {
		t.Errorf("identical commands: slices = %+v, err = %v, want the one command verbatim", slices, err)
	}
}

// TestFixSlicesGroupByWorkspaceAlone: findings of one workspace with different
// commands share a slice; findings of different workspaces never do.
func TestFixSlicesGroupByWorkspaceAlone(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	reported := []Finding{
		fixFinding("r1-f1", "alpha/a.go", "alpha", "go test ./alpha -run 'TestA'"),
		fixFinding("r1-f2", "alpha/b.go", "alpha", "go vet ./alpha"),
		fixFinding("r1-f3", "beta/c.go", "beta", "go test ./alpha -run 'TestA'"),
	}
	_, slices, err := routeRound(1, st, "T-1", nil, reported, nil, nil, oneRepoFiles(twoWorkspaceManifest()), 0, 0, 5)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("slices = %v, want one per workspace", sliceIDs(slices))
	}
	alpha := sliceByID(t, slices, "fix-1-alpha")
	if !equalStrings(alpha.Findings, []string{"r1-f1", "r1-f2"}) || alpha.Oracle != "(go test ./alpha -run 'TestA') && (go vet ./alpha)" {
		t.Errorf("alpha slice = %+v, want both findings and both commands", alpha)
	}
	if beta := sliceByID(t, slices, "fix-1-beta"); !equalStrings(beta.Findings, []string{"r1-f3"}) || beta.Oracle != "go test ./alpha -run 'TestA'" {
		t.Errorf("beta slice = %+v, want its own finding and command", beta)
	}
}

// TestAFixSliceIdCarriesNoCommand: a slice's id is the round and the
// workspace, numbered when a group splits, with a q marker for a queued keep,
// and never the command its findings named.
func TestAFixSliceIdCarriesNoCommand(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	files := oneRepoFiles(oneOracleManifest())
	reported := []Finding{
		fixFinding("r1-f1", "a.go", "root", "go test ./a"),
		fixFinding("r1-f2", "b.go", "root", "go test ./b"),
		fixFinding("r1-f3", "c.go", "root", "go test ./c"),
	}
	_, slices, err := routeRound(2, st, "T-1", nil, reported, nil, nil, files, 0, 0, 2)
	if err != nil {
		t.Fatalf("routeRound: %v", err)
	}
	if got := sliceIDs(slices); !equalStrings(got, []string{"fix-2-root-1", "fix-2-root-2"}) {
		t.Errorf("ids = %v, want fix-2-root-1 and fix-2-root-2", got)
	}
	// Each split slice carries its own findings' commands and no others.
	for id, want := range map[string]string{"fix-2-root-1": "(go test ./a) && (go test ./b)", "fix-2-root-2": "go test ./c"} {
		if got := sliceByID(t, slices, id).Oracle; got != want {
			t.Errorf("slice %s oracle = %q, want exactly %q", id, got, want)
		}
	}

	queued, err := buildFixSlices(2, st, "T-1", nil, nil, reported[:1], files, 5, true)
	if err != nil || len(queued) != 1 || queued[0].ID != "fix-2-q-root" {
		t.Errorf("queued = %+v, err = %v, want fix-2-q-root", queued, err)
	}
}

// TestFixSliceNameInAManifestOracleStaysARunnableCommand: a manifest oracle's
// name among several commands is written out as the manifest defines it, since
// inside parentheses it would be no oracle name.
func TestFixSliceNameInAManifestOracleStaysARunnableCommand(t *testing.T) {
	t.Parallel()

	man := manifest.Manifest{
		Oracles:    map[string]string{"test": "go test {path}/..."},
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
	}
	got := fixSliceOracle([]Finding{fixFinding("r1-f1", "a.go", "root", "test"), fixFinding("r1-f2", "b.go", "root", "go vet ./b")}, oneRepoFiles(man))
	if want := "(go test ./...) && (go vet ./b)"; got != want {
		t.Errorf("oracle = %q, want %q", got, want)
	}
	if got := fixSliceOracle([]Finding{fixFinding("r1-f1", "a.go", "root", "test")}, oneRepoFiles(man)); got != "test" {
		t.Errorf("a lone name = %q, want it verbatim so it runs as the manifest defines it", got)
	}
}

// TestTriageTargetRoundTripsACommandWithSpaces: the triage line's Command reads
// back the workspace and the command it wrote, whatever the command holds.
func TestTriageTargetRoundTripsACommandWithSpaces(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ workspace, oracle string }{
		{"alpha", "go test ./a -run 'TestA|TestB' && go vet ./a"},
		{"", "go test ./a -run 'TestA'"},
		{"api/lib", "! git ls-files --error-unmatch cmd/x.exe # no exe"},
		{"alpha", "workspace=beta oracle=x"},
		{"alpha", ""},
		{"", ""},
	} {
		ws, oracle := triageTarget(TriageTarget(c.workspace, c.oracle))
		if ws != c.workspace || oracle != c.oracle {
			t.Errorf("triageTarget(TriageTarget(%q, %q)) = %q, %q", c.workspace, c.oracle, ws, oracle)
		}
	}
}

// TestTriageTargetReadsAnOldLine: a line written when a command was one word
// still reads, in either order of its parts' presence.
func TestTriageTargetReadsAnOldLine(t *testing.T) {
	t.Parallel()

	for line, want := range map[string][2]string{
		"workspace=alpha oracle=test": {"alpha", "test"},
		"oracle=test":                 {"", "test"},
		"workspace=alpha":             {"alpha", ""},
		"":                            {"", ""},
	} {
		if ws, oracle := triageTarget(line); ws != want[0] || oracle != want[1] {
			t.Errorf("triageTarget(%q) = %q, %q, want %q, %q", line, ws, oracle, want[0], want[1])
		}
	}
}

// TestRecordedFindingFixRequiresACommand: `jig finding` refuses a new fix that
// names no command, and a note or an ask may name none.
func TestRecordedFindingFixRequiresACommand(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	file := p.Request.MustReview[0]
	fix := ResultFinding{File: file, Line: 1, Title: "t", Action: ActionFix, Risk: RiskLow, RiskRationale: "r"}
	if n, err := AppendDraft(d, p, Draft{Finding: &fix}); err == nil || n != 0 || !strings.Contains(err.Error(), "names no oracle") {
		t.Fatalf("a fix with no command = %d, %v, want REVIEW_INVALID saying a fix names the command", n, err)
	}
	fix.Oracle = "   "
	if _, err := AppendDraft(d, p, Draft{Finding: &fix}); err == nil {
		t.Fatal("a fix whose command is blank was taken")
	}
	for _, action := range []string{ActionAsk, ActionNote} {
		f := ResultFinding{File: file, Line: 1, Title: "t", Action: action, Risk: RiskLow, RiskRationale: "r"}
		if _, err := AppendDraft(d, p, Draft{Finding: &f}); err != nil {
			t.Errorf("a %s with no command: %v", action, err)
		}
	}
}

// TestRecordedFindingAcceptsAnyCommand: any non-empty text is a command, a
// manifest oracle's name or not, and is recorded as written.
func TestRecordedFindingAcceptsAnyCommand(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	file := p.Request.MustReview[0]
	for _, command := range []string{p.Request.Oracles[0], "go test ./internal/x -run 'TestA'", "! git ls-files --error-unmatch cmd/x.exe"} {
		f := ResultFinding{File: file, Line: 1, Title: "t", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: command}
		if _, err := AppendDraft(d, p, Draft{Finding: &f}); err != nil {
			t.Errorf("a fix naming %q: %v", command, err)
		}
	}
	drafts, err := ReadDrafts(p)
	if err != nil || len(drafts) != 3 || drafts[1].Finding.Oracle != "go test ./internal/x -run 'TestA'" {
		t.Errorf("drafts = %+v (%v), want the three, each command as written", drafts, err)
	}
}

// TestAManifestWithNoOracleStillQueuesAPersonsKeep: a keep a person recorded
// is queued as a fix slice on a manifest that declares no oracle, since the
// command it names is all the slice needs.
func TestAManifestWithNoOracleStillQueuesAPersonsKeep(t *testing.T) {
	t.Parallel()

	st := newReviewStore(t)
	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}} // zero oracles
	cum := map[string]Finding{
		"r1-f1": {ID: "r1-f1", File: "a.go", Title: "t", RiskRationale: "r", Action: ActionAsk, Oracle: "go test ./a -run 'TestA'", Workspace: "root", Status: StatusAsked},
	}
	decisions := map[string]TriageDecision{"r1-f1": {Ref: "r1-f1", Outcome: TriageKeep, Provenance: "person", Pending: true}}
	slices, kept, err := queueKeeps(1, st, "T-1", nil, cum, decisions, oneRepoFiles(man), 5)
	if err != nil {
		t.Fatalf("queueKeeps: %v", err)
	}
	if len(slices) != 1 || slices[0].ID != "fix-1-q-root" || slices[0].Oracle != "go test ./a -run 'TestA'" || !equalStrings(kept, []string{"r1-f1"}) {
		t.Fatalf("slices = %+v, kept = %v, want one fix-1-q-root with the command", slices, kept)
	}
}

// TestABlankCommandIsNoCommand: a command of only spaces names none, in every
// place that judges a build target: the gaps of a finding, a person's keep as
// status shows it, and the round that applies it.
func TestABlankCommandIsNoCommand(t *testing.T) {
	t.Parallel()

	f := Finding{ID: "r1-f1", Workspace: "root", Oracle: "  "}
	if _, noOracle := BuildTargetGaps(f); !noOracle {
		t.Error("BuildTargetGaps: a blank command is a command")
	}
	d := TriageDecision{Outcome: TriageKeep, Oracle: " "}
	if !d.KeepLacksTarget(Finding{ID: "r1-f1", Workspace: "root"}) {
		t.Error("KeepLacksTarget: a keep with a blank command reads as a full keep")
	}
	if d.KeepLacksTarget(Finding{ID: "r1-f1", Workspace: "root", Oracle: "go vet ./a"}) {
		t.Error("KeepLacksTarget: a finding that names a command needs none from the keep")
	}
	if _, kept, applied := decideFinding(Finding{ID: "r1-f1", Workspace: "root"}, AskOutcome{Keep: true, Oracle: "  "}); kept || applied {
		t.Errorf("decideFinding with a blank command: kept = %v, applied = %v, want neither", kept, applied)
	}
	if got := fixSliceOracle([]Finding{fixFinding("r1-f1", "a.go", "root", "  go vet ./a  ")}, oneRepoFiles(oneOracleManifest())); got != "go vet ./a" {
		t.Errorf("fixSliceOracle = %q, want the command trimmed", got)
	}
}
