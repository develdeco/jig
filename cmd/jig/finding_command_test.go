package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestFindingHelpSaysTheOracleIsACommand: `jig finding --help` says --oracle
// is the narrowest command that proves the fix, a literal, and required for a
// fix: not a manifest oracle's name, and not a choice among several.
func TestFindingHelpSaysTheOracleIsACommand(t *testing.T) {
	t.Parallel()
	out, code := runMain(t, testEnv(t.TempDir()), "", "finding", "--help")
	if code != 0 {
		t.Fatalf("jig finding --help: exit = %d\n%s", code, out)
	}
	for _, want := range []string{"narrowest command that proves the fix", "literal", "required for a fix", "everywhere its rule applies", "a search that must come back empty", "one command sh can run, its explanation in --detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig finding --help does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "manifest oracle that best proves") {
		t.Errorf("jig finding --help still names a manifest oracle:\n%s", out)
	}
}

// TestTriageKeepCarriesACommand: `jig triage keep --oracle <cmd>` records a
// command with spaces on the triage line, reads back as written, and makes the
// keep a full build target, so status does not flag it.
func TestTriageKeepCarriesACommand(t *testing.T) {
	t.Parallel()
	help, code := runMain(t, testEnv(t.TempDir()), "", "triage", "--help")
	if code != 0 || !strings.Contains(help, "the command that proves the fix, a literal") {
		t.Fatalf("jig triage --help: exit = %d, want 0 saying --oracle is a command\n%s", code, help)
	}

	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	writeAsks(t, fx.StoreDir, fx.Ticket, askSpec{id: "r1-f1", workspace: "alpha"})
	const command = "go test ./alpha -run 'TestA|TestB' && go vet ./alpha"
	if code := triageAs(t, fx, true, "r1-f1", "keep", "--reason", "fix it", "--oracle", command); code != 0 {
		t.Fatalf("jig triage keep --oracle: exit = %d, want 0", code)
	}
	decisions, err := verifydeliver.ReadTriageDecisions(st, fx.Ticket)
	if err != nil {
		t.Fatalf("ReadTriageDecisions: %v", err)
	}
	if d := decisions["r1-f1"]; d.Oracle != command || d.Workspace != "" || d.Outcome != verifydeliver.TriageKeep {
		t.Errorf("decision = %+v, want a keep carrying the command as written and no workspace", d)
	}
	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,keep,person`; askRow(got, "r1-f1") != want {
		t.Errorf("row = %q, want the keep unflagged: %q", askRow(got, "r1-f1"), want)
	}
}
