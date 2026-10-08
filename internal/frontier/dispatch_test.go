package frontier

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestRenderDispatchPromptMatchesGolden pins the build dispatch prompt to a
// golden text transcribed here, not derived from dispatchPromptTemplate, so
// any change to what a builder is told shows up as a diff of this text.
func TestRenderDispatchPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderDispatchPrompt("a", "JIG-1", "say hello", "go test ./alpha/...", "/abs/a.attempt-1.slice.json", "/abs/a.attempt-1.result.json", true)
	want := "You are a jig build session for slice a of ticket JIG-1.\n" +
		"Work ONLY in this worktree. Goal: say hello\n" +
		"Oracle (green = done): go test ./alpha/...\n" +
		"While you work, run only the tests that cover your change. When you report green, jig runs the oracle and hands you its output if it fails.\n" +
		"Read your inputs from slice.json at /abs/a.attempt-1.slice.json (brief: the ticket's brief.md, empty when it has none; brief sections by path, attempt log, prior answer, oracle_seconds: how long jig's last run of the oracle took on this ticket, 0 before the first, earlier_slices: what this ticket's verified slices did and the files they changed, and related: the files and symbols a code graph links to your goal, empty when the project keeps none).\n" +
		"Commit as you land. When finished write result.json at /abs/a.attempt-1.result.json with exactly one JSON object: " +
		`{"outcome": "green|code-bug|flawed-brief|oracle-wrong|blocked-by-env|needs-input|failed", "summary": "...", "commit": "<sha>", "question": "only for needs-input", "artifacts": ["relative paths"]}`
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}

	// A backend that cannot resume a session: a red oracle run fails the
	// attempt, so the builder runs the oracle once itself.
	got = renderDispatchPrompt("a", "JIG-1", "say hello", "go test ./alpha/...", "/abs/a.attempt-1.slice.json", "/abs/a.attempt-1.result.json", false)
	want = strings.Replace(want,
		"When you report green, jig runs the oracle and hands you its output if it fails.\n",
		"When you report green, jig runs the oracle, and a red run fails the attempt, so run it once yourself before you report green.\n", 1)
	if got != want {
		t.Errorf("prompt without fix turns does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderOracleFixPromptMatchesGolden pins the turn jig hands a builder's
// own session when its oracle run at a claimed green comes back red.
func TestRenderOracleFixPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderOracleFixPrompt("go test ./alpha/...", 42, "--- FAIL: TestHello", "/abs/a.attempt-1.result.json")
	want := "jig ran the oracle `go test ./alpha/...` on your commit, and it failed after 42 s. Its output ends:\n" +
		"--- FAIL: TestHello\n" +
		"Fix the cause, commit, and write result.json at /abs/a.attempt-1.result.json again, as before, with a summary of the slice's whole change."
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderDirtyTreePromptMatchesGolden pins the next turn jig hands a
// builder's own session when it reports green with uncommitted changes.
func TestRenderDirtyTreePromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderDirtyTreePrompt("go test ./alpha/...", " M alpha/alpha.go", "/abs/a.attempt-1.result.json")
	want := "jig did not run the oracle `go test ./alpha/...`: your working tree has uncommitted changes to tracked files, so the commit you reported is not what a run would test:\n" +
		" M alpha/alpha.go\n" +
		"Commit or revert them, and write result.json at /abs/a.attempt-1.result.json again, as before, with a summary of the slice's whole change."
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRunNamesTheTicketsBriefInEverySliceJSON: slice.json's brief is the
// absolute store-side path of the ticket's brief.md for every slice, whether
// or not the slice builds from any of its sections. A gate fix slice cites
// none (it has no from_brief), yet its goal is findings that cite
// brief.md#<section>; with no path a builder searched the disk for the file.
// A ticket with no brief.md gets "".
func TestRunNamesTheTicketsBriefInEverySliceJSON(t *testing.T) {
	t.Parallel()

	t.Run("a slice with no from_brief on a ticket that has a brief", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		slices, err := st.ReadSlices(fx.Ticket)
		if err != nil {
			t.Fatalf("ReadSlices: %v", err)
		}
		for i := range slices {
			if slices[i].ID == "a" {
				slices[i].FromBrief = nil
			}
		}
		data, err := yaml.Marshal(store.SliceFile{Slices: slices})
		if err != nil {
			t.Fatalf("marshal slices: %v", err)
		}
		if err := os.WriteFile(filepath.Join(st.TicketDir(fx.Ticket), "slices.yaml"), data, 0o644); err != nil {
			t.Fatalf("write slices.yaml: %v", err)
		}
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		want, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "brief.md"))
		if err != nil || len(want) == 0 {
			t.Fatalf("test setup: the ticket must have a brief: %v", err)
		}
		a := readSliceJSON(t, st, fx.Ticket, "a")
		if len(a.BriefSections) != 0 {
			t.Fatalf("a's brief_sections = %v, want none: it builds from no section", a.BriefSections)
		}
		if !filepath.IsAbs(a.Brief) {
			t.Fatalf("a's brief = %q, want an absolute path", a.Brief)
		}
		got, err := os.ReadFile(a.Brief)
		if err != nil {
			t.Fatalf("a's brief %q does not name a readable file: %v", a.Brief, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("a's brief %q holds %q, want the ticket's brief.md %q", a.Brief, got, want)
		}

		// A slice that does build from sections is told the same path, and its
		// section paths are under it.
		b := readSliceJSON(t, st, fx.Ticket, "b")
		if b.Brief != a.Brief {
			t.Errorf("b's brief = %q, want a's %q: the ticket has one brief", b.Brief, a.Brief)
		}
		if len(b.BriefSections) == 0 {
			t.Fatalf("b's brief_sections is empty, want its sections")
		}
		for _, sec := range b.BriefSections {
			if !strings.HasPrefix(sec, b.Brief+"#") {
				t.Errorf("b's brief section %q is not under its brief %q", sec, b.Brief)
			}
		}
	})

	t.Run("a ticket with no brief.md", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
			t.Fatalf("remove brief.md: %v", err)
		}
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := readSliceJSON(t, st, fx.Ticket, "a"); got.Brief != "" {
			t.Errorf("a's brief = %q, want \"\": the ticket has no brief.md", got.Brief)
		}
		// The field is present, as "", not left out.
		raw, err := os.ReadFile(sliceJSONPath(st, fx.Ticket, "a", 1))
		if err != nil {
			t.Fatalf("read a's slice.json: %v", err)
		}
		if !strings.Contains(string(raw), `"brief":""`) {
			t.Errorf("a's slice.json = %s, want a \"brief\":\"\" field", raw)
		}
	})
}
