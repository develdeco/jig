package e2e

// End-to-end tests of the correction notes: a slice that fails its oracle the
// same way twice, a brief that changed under verified work, and a commit
// outside the workspaces of the slices. The scripted harness works in the
// fixture clone and fires the hooks a real one fires at jig observe; the
// notes come back where a real harness reads them, and under an unattended
// claim (or the project default) the Stop hook holds the session back once
// for each.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// corrRig is a session s-corr on the fixture ticket branch, started.
func corrRig(t *testing.T) (*fixture.Fixture, jigEnv, *fakeHarness) {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-corr")
	h.Branch("jig/" + fx.Ticket)
	if res := h.Hook("SessionStart", map[string]any{"source": "startup"}); res.Code != 0 {
		t.Fatalf("SessionStart exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	return fx, env, h
}

// corrContext is the additionalContext a hook printed, failing the test when
// it printed nothing.
func corrContext(t *testing.T, res jigResult) string {
	t.Helper()
	out := decodeHook(t, res)
	if out.Specific.Context == "" {
		t.Fatalf("the hook printed no context: %q", res.Stdout)
	}
	return out.Specific.Context
}

// corrSilent fails the test unless the hook printed nothing.
func corrSilent(t *testing.T, what string, res jigResult) {
	t.Helper()
	if res.Code != 0 || res.Stdout != "" {
		t.Errorf("%s: exit %d, stdout %q, want silence", what, res.Code, res.Stdout)
	}
}

// corrLines lists the correction lines of the ticket journal.
func corrLines(t *testing.T, fx *fixture.Fixture) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventCorrection {
			out = append(out, l)
		}
	}
	return out
}

// corrLine returns the one correction line of the outcome (the kind), failing
// the test unless there is exactly one.
func corrLine(t *testing.T, fx *fixture.Fixture, kind string) journal.Line {
	t.Helper()
	var got []journal.Line
	for _, l := range corrLines(t, fx) {
		if l.Outcome == kind {
			got = append(got, l)
		}
	}
	if len(got) != 1 {
		t.Fatalf("correction lines of kind %s = %+v, want exactly one", kind, got)
	}
	return got[0]
}

// corrDelivered lists the refs of the note-delivered ledger lines of the
// session with the outcome (prompt, tool or stop-block).
func corrDelivered(t *testing.T, env jigEnv, outcome string) []string {
	t.Helper()
	var refs []string
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventNoteDelivered && l.Session == "s-corr" && l.Outcome == outcome {
			refs = append(refs, l.Ref)
		}
	}
	return refs
}

// corrCommit writes the file (clone-relative) and commits it as the agent
// does, through its Bash tool, with an optional Jig-Slice trailer. It
// returns the commit and what the PostToolUse hook of the commit printed.
func corrCommit(t *testing.T, h *fakeHarness, file, slice string) (string, jigResult) {
	t.Helper()
	full := filepath.Join(h.Clone, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, full, []byte("content of "+file+"\n"))
	if add := h.Bash("git add " + file); add.Code != 0 {
		t.Fatalf("git add %s: exit %d: %s", file, add.Code, add.Stderr)
	}
	cmd := "git commit -m 'work on " + file + "'"
	if slice != "" {
		cmd += " -m 'Jig-Slice: " + slice + "'"
	}
	res := h.Bash(cmd)
	if res.Code != 0 {
		t.Fatalf("%s: exit %d: %s", cmd, res.Code, res.Stderr)
	}
	return h.git("rev-parse", "HEAD"), res.Hook
}

// corrSeed appends lines to the ticket journal as the verify workers would
// have, so a test need not run an oracle for each.
func corrSeed(t *testing.T, fx *fixture.Fixture, lines ...journal.Line) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
}

// corrVerified is the verified line of slice a at a made-up commit.
func corrVerified() journal.Line {
	return journal.Line{Slice: "a", Event: journal.EventVerified, Commit: "cafe00cafe00cafe00cafe00cafe00cafe00cafe", Attempt: 1, Session: "s-corr"}
}

// corrFailures are two failed oracle lines of slice a with one signature.
func corrFailures() []journal.Line {
	var out []journal.Line
	for attempt := 2; attempt <= 3; attempt++ {
		out = append(out, journal.Line{
			Slice: "a", Event: journal.EventOracle, Outcome: "fail", Attempt: attempt, Source: "jig", Session: "s-corr",
			Commit:    "feed00feed00feed00feed00feed00feed00fee" + string(rune('0'+attempt)),
			Summary:   "FAIL TestClamp: Clamp returns 10 for Clamp(10, 0, 5)",
			Signature: "a|code-bug|fail testclamp: clamp returns for clamp(, , )",
		})
	}
	return out
}

// corrEditBrief replaces old with replacement in the ticket brief of the store.
func corrEditBrief(t *testing.T, fx *fixture.Fixture, old, replacement string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.TicketDir(fx.Ticket), "brief.md")
	body := string(readFileOrFatal(t, path))
	if !strings.Contains(body, old) {
		t.Fatalf("test setup: the brief lacks %q:\n%s", old, body)
	}
	writeFileOrFatal(t, path, []byte(strings.Replace(body, old, replacement, 1)))
}

// corrMarkUnattended makes the project default to unattended, with the
// jig.yaml marker of the clone.
func corrMarkUnattended(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(fx.RepoDir, project.MarkerFile), []byte("jig: \"1\"\nproject: "+cfg.Name+"\nunattended: true\n"))
}

// TestCorrectionStall: slice a is committed twice by the session and fails its
// oracle the same way both times, in a verification of its own each. The first
// failure is no stall. After the second, the next prompt tells the session so,
// once; a correction line records it; and, with a person at the keyboard, the
// slice is left as the verification left it.
func TestCorrectionStall(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	failed := func(sha string) journal.Line {
		return waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
			return l.Event == journal.EventOracle && l.Slice == "a" && l.Commit == sha
		})
	}

	first, hook := corrCommit(t, h, "alpha/try1.txt", "a")
	corrSilent(t, "the PostToolUse of the first commit", hook)
	one := failed(first)
	h.WaitVerifiers()
	if one.Outcome != "fail" || one.Signature == "" {
		t.Fatalf("oracle line = %+v, want a failure with a signature", one)
	}
	if res := h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}); res.Code != 0 || strings.Contains(res.Stdout, "same way") {
		t.Errorf("after one failure the prompt printed %q, want no stall", res.Stdout)
	}
	if lines := corrLines(t, fx); len(lines) != 0 {
		t.Fatalf("one failure left correction lines %+v", lines)
	}

	// The same tree plus a file that changes nothing the test looks at: the
	// same failure.
	second, hook := corrCommit(t, h, "alpha/try2.txt", "a")
	corrSilent(t, "the PostToolUse of the second commit", hook)
	two := failed(second)
	h.WaitVerifiers()
	if two.Signature != one.Signature {
		t.Fatalf("the two failures have signatures %q and %q, want one: the test setup changed what fails", one.Signature, two.Signature)
	}

	ctx := corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "and again"}))
	for _, want := range []string{"slice a failed its oracle the same way 2 times", "attempt 2", second[:12], "change the approach"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("prompt context = %q, want %q", ctx, want)
		}
	}
	if strings.Contains(ctx, "jig requeue") {
		t.Errorf("prompt context = %q: a slice that is not stalled has nothing to requeue", ctx)
	}
	line := corrLine(t, fx, "stall")
	if line.Slice != "a" || line.Session != "s-corr" || line.Harness != "claude" || line.Provenance != "jig" || line.Signature != two.Signature ||
		line.Commit != second || !strings.HasPrefix(line.Ref, "stall:a:") || line.Summary == "" {
		t.Errorf("correction line = %+v, want the stall of slice a at %s by session s-corr", line, second)
	}
	if refs := corrDelivered(t, env, "prompt"); !slices.Contains(refs, line.Ref) {
		t.Errorf("note-delivered refs = %v, want %s among them", refs, line.Ref)
	}
	corrSilent(t, "a second prompt", h.Hook("UserPromptSubmit", map[string]any{"prompt": "once more"}))
	if got := len(corrLines(t, fx)); got != 1 {
		t.Errorf("correction lines = %d after the repeat was seen again, want 1", got)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "building" || ss.Reason != "" {
		t.Errorf("slice a = %+v, %v, want building: a person at the keyboard stalls nothing", ss, err)
	}
}

// TestCorrectionBriefDrift: slice a is verified. Editing the brief section of
// slice b, which no one verified, is no drift; editing the section slice a
// was cut from is. The next prompt tells the session once, a correction line
// records it, and the remedy the note names (a requeue of the slices that
// cite the sections that changed) makes the note stop.
func TestCorrectionBriefDrift(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)
	sha := h.CommitScenario("a", 1)
	res := h.Jig("verify", fx.Ticket, sha, "--slice", "a", "--wait")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify --wait exit = %d, want a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	corrEditBrief(t, fx, "Add a small percentage-range caller", "Add two small percentage-range callers")
	ctx := corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}))
	if !strings.Contains(ctx, "verified "+sha[:12]+" for slice a") || strings.Contains(ctx, "brief changed") {
		t.Fatalf("prompt context = %q, want the verification of slice a and no drift: the edited section is slice b, not verified", ctx)
	}
	if lines := corrLines(t, fx); len(lines) != 0 {
		t.Fatalf("an edit of a section no verified slice cites left correction lines %+v", lines)
	}

	corrEditBrief(t, fx, "without changing its signature.", "without changing its signature or its tests.")
	ctx = corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "and now"}))
	for _, want := range []string{"the brief changed under verified work", "slice a cites 1 section", "jig requeue " + fx.Ticket + " --from-brief-diff"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("prompt context = %q, want %q", ctx, want)
		}
	}
	line := corrLine(t, fx, "brief-drift")
	if line.Session != "s-corr" || line.Provenance != "jig" || !strings.HasPrefix(line.Ref, "brief:") || !strings.Contains(line.Summary, "slice a cites") {
		t.Errorf("correction line = %+v, want the drift, by session s-corr", line)
	}
	if refs := corrDelivered(t, env, "prompt"); !slices.Contains(refs, line.Ref) {
		t.Errorf("note-delivered refs = %v, want %s among them", refs, line.Ref)
	}
	corrSilent(t, "a second prompt", h.Hook("UserPromptSubmit", map[string]any{"prompt": "again"}))

	// The remedy: the slice goes back to the queue, and what it was verified
	// against no longer stands for it.
	if res := h.Jig("requeue", fx.Ticket, "--from-brief-diff"); res.Code != 0 || !strings.Contains(res.Stdout, "requeued") {
		t.Fatalf("jig requeue --from-brief-diff exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	corrSilent(t, "a prompt after the requeue", h.Hook("UserPromptSubmit", map[string]any{"prompt": "after the requeue"}))
	if got := len(corrLines(t, fx)); got != 1 {
		t.Errorf("correction lines = %d after the requeue, want the one", got)
	}
}

// TestCorrectionScopeDrift: a commit inside the workspaces of the slices says
// nothing; a commit that also changes files outside them is told to the session
// by the PostToolUse hook of the commit that made it, once, and recorded with
// the files.
func TestCorrectionScopeDrift(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)

	_, hook := corrCommit(t, h, "alpha/inside.txt", "")
	corrSilent(t, "the PostToolUse of a commit inside the workspaces", hook)
	if lines := corrLines(t, fx); len(lines) != 0 {
		t.Fatalf("a commit inside the workspaces left correction lines %+v", lines)
	}

	sha, hook := corrCommit(t, h, "docs/notes.md", "")
	ctx := corrContext(t, hook)
	for _, want := range []string{"commit " + sha[:12], "1 file outside every workspace", fx.Ticket, "alpha, beta", "docs/notes.md"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("PostToolUse context = %q, want %q", ctx, want)
		}
	}
	line := corrLine(t, fx, "scope-drift")
	if line.Ref != "scope:"+sha[:12] || line.Commit != sha || len(line.Files) != 1 || line.Files[0] != "docs/notes.md" || line.Session != "s-corr" || line.Provenance != "jig" {
		t.Errorf("correction line = %+v, want the drift of %s on docs/notes.md by session s-corr", line, sha)
	}
	if refs := corrDelivered(t, env, "tool"); len(refs) != 1 || refs[0] != line.Ref {
		t.Errorf("note-delivered refs = %v, want %s once", refs, line.Ref)
	}
	corrSilent(t, "the prompt after it", h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}))

	// A second commit that reaches outside is a drift of its own.
	sha2, hook := corrCommit(t, h, "README.md", "")
	if ctx := corrContext(t, hook); !strings.Contains(ctx, "commit "+sha2[:12]) || strings.Contains(ctx, sha[:12]) {
		t.Errorf("PostToolUse context = %q, want the second commit %s only", ctx, sha2)
	}
	if got := len(corrLines(t, fx)); got != 2 {
		t.Errorf("correction lines = %d, want 2", got)
	}
}

// corrAllThree puts the ticket into all three conditions at once: slice a is
// verified and then fails its oracle twice the same way, the brief section it
// was cut from is edited, and the session commits a file outside the
// workspaces. It returns what the next prompt printed (the stall and the brief
// drift are told there: it is the first hook that sees them), the commit and
// what the PostToolUse hook of that commit printed (the scope drift).
func corrAllThree(t *testing.T, fx *fixture.Fixture, h *fakeHarness) (prompt jigResult, sha string, commit jigResult) {
	t.Helper()
	corrSeed(t, fx, append([]journal.Line{corrVerified()}, corrFailures()...)...)
	corrEditBrief(t, fx, "without changing its signature.", "without changing its signature or its tests.")
	prompt = h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"})
	sha, commit = corrCommit(t, h, "docs/outside.md", "")
	return prompt, sha, commit
}

// The fragments of the three notes, as a session reads them.
const (
	corrStallText = "slice a failed its oracle the same way 2 times"
	corrBriefText = "the brief changed under verified work"
	corrScopeText = "outside every workspace"
)

// TestCorrectionNeverBlocksInteractive: with a person at the keyboard (no
// unattended claim, no project default) the three corrections are only told:
// the hooks give them to the model, the Stop hook shows them to the person,
// and neither it nor a stop that is already continuing is ever refused. The
// slice is not marked stalled.
func TestCorrectionNeverBlocksInteractive(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)
	prompt, sha, commit := corrAllThree(t, fx, h)

	ctx := corrContext(t, prompt)
	for _, want := range []string{corrStallText, corrBriefText} {
		if !strings.Contains(ctx, want) {
			t.Errorf("prompt context = %q, want %q", ctx, want)
		}
	}
	if ctx := corrContext(t, commit); !strings.Contains(ctx, corrScopeText) || !strings.Contains(ctx, "commit "+sha[:12]) {
		t.Errorf("PostToolUse context = %q, want the scope drift of %s", ctx, sha)
	}
	for _, active := range []bool{false, true, false} {
		stop := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": active}))
		if stop.Decision != "" || stop.Reason != "" {
			t.Fatalf("Stop (active %v) = %+v, want no refusal: nobody asked for one", active, stop)
		}
		for _, want := range []string{corrStallText, corrBriefText, corrScopeText} {
			if !strings.Contains(stop.SystemMessage, want) {
				t.Errorf("Stop (active %v) message = %q, want it to show %q to the person", active, stop.SystemMessage, want)
			}
		}
	}
	if refs := corrDelivered(t, env, "stop-block"); len(refs) != 0 {
		t.Errorf("stop-block lines = %v, want none", refs)
	}
	if got := len(corrLines(t, fx)); got != 3 {
		t.Errorf("correction lines = %d, want 3 (stall, brief drift, scope drift), each once however often the hooks looked", got)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "queued" {
		t.Errorf("slice a = %+v, %v, want queued: a stall is only marked when nobody is watching", ss, err)
	}
}

// TestCorrectionBlocksUnattendedOnce: when the project defaults to unattended
// the same three corrections hold the session back at Stop, once each: the
// first Stop is refused with all of them and the remedy of the stall, the
// slice is marked stalled, a Stop that is continuing for them and the Stop
// after it are let through, and a correction made later is refused for on its
// own, once.
func TestCorrectionBlocksUnattendedOnce(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)
	corrMarkUnattended(t, fx)
	prompt, _, commit := corrAllThree(t, fx, h)

	// The model hears them all the same, at the first hook that sees each.
	for _, c := range []struct {
		hook jigResult
		want []string
	}{{prompt, []string{corrStallText, corrBriefText}}, {commit, []string{corrScopeText}}} {
		ctx := corrContext(t, c.hook)
		for _, want := range c.want {
			if !strings.Contains(ctx, want) {
				t.Errorf("context = %q, want %q", ctx, want)
			}
		}
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "stalled" || ss.Reason != "stall" || ss.Signature == "" || ss.StallSummary == "" || ss.Attempts != 3 {
		t.Errorf("slice a = %+v, %v, want stalled for a stall, with its signature and summary", ss, err)
	}

	stop := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if stop.Decision != "block" {
		t.Fatalf("Stop = %+v, want a refusal", stop)
	}
	for _, want := range []string{"this session is unattended", corrStallText, "jig requeue " + fx.Ticket + " --slice a", corrBriefText, "jig requeue " + fx.Ticket + " --from-brief-diff", corrScopeText} {
		if !strings.Contains(stop.Reason, want) {
			t.Errorf("Stop reason = %q, want %q", stop.Reason, want)
		}
	}
	refused := map[string]bool{}
	for _, ref := range corrDelivered(t, env, "stop-block") {
		refused[ref] = true
	}
	for _, l := range corrLines(t, fx) {
		if !refused[l.Ref] {
			t.Errorf("correction %s (%s) was not recorded as refused for; refused for %v", l.Ref, l.Outcome, refused)
		}
	}

	corrSilent(t, "the Stop that is continuing for them", h.Hook("Stop", map[string]any{"stop_hook_active": true}))
	corrSilent(t, "the Stop after it", h.Hook("Stop", map[string]any{"stop_hook_active": false}))

	// A new correction is a new fact: refused for once, and only for it.
	sha2, hook := corrCommit(t, h, "docs/more.md", "")
	if ctx := corrContext(t, hook); !strings.Contains(ctx, "commit "+sha2[:12]) {
		t.Errorf("PostToolUse context = %q, want the new commit %s", ctx, sha2)
	}
	stop = decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": true}))
	if stop.Decision != "block" || !strings.Contains(stop.Reason, "commit "+sha2[:12]) || strings.Contains(stop.Reason, corrStallText) || strings.Contains(stop.Reason, corrBriefText) {
		t.Errorf("Stop for a new correction = %+v, want a refusal for the commit %s alone", stop, sha2)
	}
	corrSilent(t, "the Stop after the new one", h.Hook("Stop", map[string]any{"stop_hook_active": true}))
	if got := len(corrLines(t, fx)); got != 4 {
		t.Errorf("correction lines = %d, want 4", got)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "stalled" {
		t.Errorf("slice a = %+v, %v, want it still stalled", ss, err)
	}
}
