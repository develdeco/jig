package e2e

// End-to-end tests of `jig validate --restamp` and `jig slice add` through the
// real binary: a brief edit and a new slice need no hand edit of slices.yaml.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// editSection appends a line to the body of the brief section heading, in
// the store's working tree, as a person adding a decision would.
func editSection(t *testing.T, fx *fixture.Fixture, heading, line string) {
	t.Helper()
	path := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "brief.md")
	brief := string(readFileOrFatal(t, path))
	marker := "## " + heading + "\n"
	i := strings.Index(brief, marker)
	if i < 0 {
		t.Fatalf("brief has no section %q", heading)
	}
	rest := brief[i+len(marker):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		end = len(rest)
	}
	edited := brief[:i+len(marker)] + strings.TrimRight(rest[:end], "\n") + "\n\n" + line + "\n" + rest[end:]
	writeFileOrFatal(t, path, []byte(edited))
}

func storeSlicesYAML(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	return string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "slices.yaml")))
}

// TestRestampKeepsAGreenSliceGreenAndSliceAddNeedsNoHandEdit: slice a is
// built green on an approved brief; a decision is added to a's section; the
// restamp makes the hashes match, a stays green with its attempts and
// verified commit, and the brief is unapproved until validate runs again. A
// slice added with `jig slice add` validates and is ready.
func TestRestampKeepsAGreenSliceGreenAndSliceAddNeedsNoHandEdit(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-restamp")
	if r := h.Jig("validate", fx.Ticket); r.Code != 0 {
		t.Fatalf("jig validate: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	h.Branch(fx.Branch)
	sha := h.CommitScenario("a", 1)
	h.Push(fx.Branch)
	if r := h.Jig("verify", fx.Ticket, sha, "--slice", "a"); r.Code != 0 {
		t.Fatalf("jig verify: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventVerified && l.Slice == "a" && l.Commit == sha
	})
	h.WaitVerifiers()

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil || before.State != "green" {
		t.Fatalf("slice a = %+v, %v, want green", before, err)
	}
	// The store's checkpoint commits the brief the slices were stamped from.
	if err := st.Push("approved brief"); err != nil {
		t.Fatal(err)
	}

	editSection(t, fx, "Slice A - failing clamp", "Decision: the clamp keeps its signature.")
	if r := h.Jig("validate", fx.Ticket, "--check"); r.Code == 0 || !strings.Contains(r.Stdout, "--restamp") {
		t.Fatalf("validate --check on the edited brief: exit %d, want a stale stamp with the restamp hint\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r := h.Jig("validate", fx.Ticket, "--restamp")
	if r.Code != 0 || !strings.Contains(r.Stdout, "a,green,") || !strings.Contains(r.Stdout, "not recorded (--restamp)") {
		t.Fatalf("validate --restamp: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	after, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil || after.State != "green" || after.Attempts != before.Attempts {
		t.Fatalf("slice a after the restamp = %+v, %v, want green with %d attempts", after, err, before.Attempts)
	}
	if got := journal.BuiltCommits(readJournal(t, fx, fx.Ticket)); len(got) != 1 || got[0] != sha {
		t.Fatalf("verified commits = %v, want %s", got, sha)
	}
	if r := h.Jig("validate", fx.Ticket, "--check"); r.Code != 0 {
		t.Fatalf("validate --check after the restamp: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}

	// The edit withdrew the approval: an unattended claim is refused until validate.
	if r := h.Jig("claim", fx.Ticket, "--slice", "b", "--unattended"); r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "CLAIM_NOT_APPROVED") {
		t.Fatalf("claim of an unapproved brief: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := h.Jig("validate", fx.Ticket); r.Code != 0 {
		t.Fatalf("jig validate: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}

	r = h.Jig("slice", "add", fx.Ticket, "--id", "extra", "--goal", "More work.", "--oracle", "test", "--workspace", "alpha", "--blocked-by", "a", "--from-brief", "Goal")
	if r.Code != 0 {
		t.Fatalf("jig slice add: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := h.Jig("validate", fx.Ticket, "--check"); r.Code != 0 {
		t.Fatalf("validate --check with the added slice: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := h.Jig("next", fx.Ticket); r.Code != 0 || !strings.Contains(r.Stdout, "extra") {
		t.Fatalf("jig next does not list the added slice: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
}

// TestRestampRefusedWhileASliceIsBuilding: a held claim on a stale slice
// blocks the restamp, and slices.yaml is left byte for byte.
func TestRestampRefusedWhileASliceIsBuilding(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-building")
	if r := h.Jig("validate", fx.Ticket); r.Code != 0 {
		t.Fatalf("jig validate: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := h.Jig("claim", fx.Ticket, "--slice", "c", "--unattended"); r.Code != 0 {
		t.Fatalf("jig claim: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Push("approved brief"); err != nil {
		t.Fatal(err)
	}
	editSection(t, fx, "Slice C - greeting decision", "Decision: formal.")
	yamlBefore := storeSlicesYAML(t, fx)
	r := h.Jig("validate", fx.Ticket, "--restamp")
	if r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "SLICE_BUILDING") {
		t.Fatalf("validate --restamp under a builder: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if storeSlicesYAML(t, fx) != yamlBefore {
		t.Fatal("a refused restamp changed slices.yaml")
	}
}
