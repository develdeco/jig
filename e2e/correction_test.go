package e2e

// End-to-end tests of the correction notes: a slice that fails its oracle the
// same way twice, a brief that changed under verified work, a commit outside
// the workspaces of the slices, and a commit that names no slice. The scripted
// harness works in the fixture clone and fires the hooks a real one fires at
// jig observe; the notes come back where a real harness reads them, and under
// an unattended claim (or the project default) the Stop hook holds the session
// back once for each.

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
	return corrRigAfter(t, nil)
}

// corrRigAfter is corrRig with the setup run on the ticket branch before the
// session starts, so that what it commits is already there when the session
// begins to watch the branch.
func corrRigAfter(t *testing.T, setup func(*testing.T, *fixture.Fixture, *fakeHarness)) (*fixture.Fixture, jigEnv, *fakeHarness) {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-corr")
	h.Branch("jig/" + fx.Ticket)
	if setup != nil {
		setup(t, fx, h)
	}
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

// corrLines lists the correction lines of the ticket journal that are the
// stall, the brief drift and the scope drift. The commits of the tests of
// those carry no trailer, so each also leaves a slice-missing line, which
// corrSliceLines lists.
func corrLines(t *testing.T, fx *fixture.Fixture) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range corrAllLines(t, fx) {
		if l.Outcome != "slice-missing" {
			out = append(out, l)
		}
	}
	return out
}

// corrSliceLines lists the slice-missing correction lines of the ticket
// journal.
func corrSliceLines(t *testing.T, fx *fixture.Fixture) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range corrAllLines(t, fx) {
		if l.Outcome == "slice-missing" {
			out = append(out, l)
		}
	}
	return out
}

// corrAllLines lists the correction lines of the ticket journal.
func corrAllLines(t *testing.T, fx *fixture.Fixture) []journal.Line {
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
	for _, l := range corrAllLines(t, fx) {
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
// session with the outcome (prompt, tool or stop-block), other than those of
// the slice-missing notes, which corrDeliveredSlice lists.
func corrDelivered(t *testing.T, env jigEnv, outcome string) []string {
	t.Helper()
	var refs []string
	for _, ref := range corrDeliveredAll(t, env, outcome) {
		if !strings.HasPrefix(ref, "slice:") {
			refs = append(refs, ref)
		}
	}
	return refs
}

// corrDeliveredSlice lists the refs of the slice-missing notes delivered to the
// session with the outcome.
func corrDeliveredSlice(t *testing.T, env jigEnv, outcome string) []string {
	t.Helper()
	var refs []string
	for _, ref := range corrDeliveredAll(t, env, outcome) {
		if strings.HasPrefix(ref, "slice:") {
			refs = append(refs, ref)
		}
	}
	return refs
}

// corrDeliveredAll lists the refs of the note-delivered ledger lines of the
// session with the outcome.
func corrDeliveredAll(t *testing.T, env jigEnv, outcome string) []string {
	t.Helper()
	var refs []string
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventNoteDelivered && l.Session == "s-corr" && l.Outcome == outcome {
			refs = append(refs, l.Ref)
		}
	}
	return refs
}

// corrSliceOnly fails the test unless the hook of the commit sha told the
// session that the commit names no slice, and nothing of the other
// corrections. The commits of the tests of those carry no trailer and the
// session claims no slice, so jig has nothing to verify them for.
func corrSliceOnly(t *testing.T, what, sha string, res jigResult) {
	t.Helper()
	ctx := corrContext(t, res)
	if !strings.Contains(ctx, "commit "+sha[:12]+" on") || !strings.Contains(ctx, "names no slice") {
		t.Errorf("%s: context = %q, want the note that commit %s names no slice", what, ctx, sha[:12])
	}
	for _, other := range []string{corrStallText, corrBriefText, corrScopeText} {
		if strings.Contains(ctx, other) {
			t.Errorf("%s: context = %q, want nothing of %q", what, ctx, other)
		}
	}
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

// TestCorrectionStall: the session commits twice for slice a, and each commit
// fails the oracle the same way when jig verifies it. The first failure is no
// stall. After the second, the next prompt tells the session so, once; a
// correction line records it; and, with a person at the keyboard, the slice is
// left as the verifications left it. The verifications are real, run in line
// (jig verify --wait) because the stall is what is under test and not the
// detached worker, which TestHooksDriveVerify covers.
func TestCorrectionStall(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	// verify commits file, which changes nothing the test looks at, so every
	// commit fails the same way, and verifies the commit.
	verify := func(file string) (string, journal.Line) {
		t.Helper()
		sha, hook := corrCommit(t, h, file, "")
		corrSliceOnly(t, "the PostToolUse of the commit of "+file, sha, hook)
		res := h.Jig("verify", fx.Ticket, sha, "--slice", "a", "--wait")
		if res.Code != 1 || !strings.Contains(res.Stdout, "state: fail") {
			t.Fatalf("jig verify --wait exit = %d, want 1 and a failure\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
		}
		for _, l := range readJournal(t, fx, fx.Ticket) {
			if l.Event == journal.EventOracle && l.Commit == sha {
				return sha, l
			}
		}
		t.Fatalf("the journal has no oracle line for %s", sha)
		return "", journal.Line{}
	}

	_, one := verify("alpha/try1.txt")
	if one.Outcome != "fail" || one.Signature == "" {
		t.Fatalf("oracle line = %+v, want a failure with a signature", one)
	}
	if res := h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}); res.Code != 0 || strings.Contains(res.Stdout, "same way") {
		t.Errorf("after one failure the prompt printed %q, want no stall", res.Stdout)
	}
	if lines := corrLines(t, fx); len(lines) != 0 {
		t.Fatalf("one failure left correction lines %+v", lines)
	}

	second, two := verify("alpha/try2.txt")
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
// nothing of scope; a commit that also changes files outside them is told to
// the session by the PostToolUse hook of the commit that made it, once, and
// recorded with the files.
func TestCorrectionScopeDrift(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRig(t)

	inside, hook := corrCommit(t, h, "alpha/inside.txt", "")
	corrSliceOnly(t, "the PostToolUse of a commit inside the workspaces", inside, hook)
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

// corrSoloSlice gives the ticket slice e, which builds in a workspace of its
// own, delta: a package that tests green. The slices of the fixture ticket
// share theirs (a and b build in alpha, c and d in beta), so a commit in
// either points to two slices and not to one. It is the setup of corrRigAfter,
// so the session starts with the commit that adds the workspace behind it.
func corrSoloSlice(t *testing.T, fx *fixture.Fixture, h *fakeHarness) {
	t.Helper()
	manifest := filepath.Join(h.Clone, ".claude", "jig.yaml")
	body := string(readFileOrFatal(t, manifest))
	const beta = "    - id: beta\n      path: beta\n"
	if !strings.Contains(body, beta) {
		t.Fatalf("test setup: the manifest lacks the beta workspace:\n%s", body)
	}
	writeFileOrFatal(t, manifest, []byte(strings.Replace(body, beta, beta+"    - id: delta\n      path: delta\n", 1)))
	if err := os.MkdirAll(filepath.Join(h.Clone, "delta"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(h.Clone, "delta", "delta.go"), []byte("package delta\n"))
	h.git("add", ".claude/jig.yaml", "delta/delta.go")
	h.git("commit", "-m", "manifest: the delta workspace")

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	all, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{{ID: "e", Workspace: "delta", Goal: "Add a delta.", Oracle: "test", BlockedBy: []string{}, FromBrief: all[0].FromBrief}}); err != nil {
		t.Fatal(err)
	}
}

// corrVerifyLines lists the journal lines that say a verification of the
// commit began or ended, for any slice.
func corrVerifyLines(t *testing.T, fx *fixture.Fixture, sha string) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		switch l.Event {
		case journal.EventVerifyPending, journal.EventOracle, journal.EventVerified:
			if l.Commit == sha {
				out = append(out, l)
			}
		}
	}
	return out
}

// TestCommitWithoutSliceGetsANote: a commit on the ticket branch with no
// Jig-Slice trailer is never verified, and the session is told so by the hook
// of the commit, once, with the slice its files point to; a commit with the
// trailer is verified and draws no note. The note is an inference: nothing is
// verified for the commit, and no slice moves. When the project defaults to
// unattended the note is enforced like the other corrections: the first Stop is
// refused with it, once, and a commit made later is refused for on its own. A
// commit put right by saying its slice to jig verify is no longer held against
// the session.
//
// This is the one chain of the note through the hooks. Which slices a commit's
// files point to (a workspace of one slice, one two slices share, none, the
// deepest of nested ones), which commits are passed over (a merge, an empty
// commit, one with the trailer), what the note says in each case and that it
// is told once are internal/correct's: TestOwnersAreTheSlicesOfTheDeepest
// WorkspaceThatHoldsAFile, TestUnslicedNamesTheSlicesTheFilesPointTo,
// TestUnslicedPassesOverMergesEmptyAndRootCommits,
// TestSliceTextNamesTheSliceTheCandidatesOrNone and the TestCheck tests of
// slice_test.go.
func TestCommitWithoutSliceGetsANote(t *testing.T) {
	t.Parallel()
	fx, env, h := corrRigAfter(t, corrSoloSlice)

	// The workspace of slice e alone.
	first, hook := corrCommit(t, h, "delta/one.txt", "")
	ctx := corrContext(t, hook)
	for _, want := range []string{
		"commit " + first[:12] + " on " + fx.Ticket + " names no slice", "jig verifies nothing for it", "delta/ (slice e's workspace)",
		"amend it with `Jig-Slice: e`", "jig verify " + fx.Ticket + " " + first[:12] + " --slice <slice>",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("PostToolUse context = %q, want %q", ctx, want)
		}
	}
	line := corrLine(t, fx, "slice-missing")
	if line.Ref != "slice:"+first[:12] || line.Commit != first || line.Slice != "" || len(line.Files) != 1 || line.Files[0] != "delta/one.txt" ||
		line.Session != "s-corr" || line.Provenance != "jig" || !strings.Contains(ctx, line.Summary) {
		t.Errorf("correction line = %+v, want the note of %s on delta/one.txt by session s-corr", line, first)
	}
	if refs := corrDeliveredSlice(t, env, "tool"); len(refs) != 1 || refs[0] != line.Ref {
		t.Errorf("note-delivered refs = %v, want %s once", refs, line.Ref)
	}
	corrSilent(t, "the prompt after it", h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}))
	if lines := corrVerifyLines(t, fx, first); len(lines) != 0 {
		t.Errorf("journal lines of a verification of %s: %+v, want none: the note only guesses", first, lines)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "e"); err != nil || ss.State != "queued" {
		t.Errorf("slice e = %+v, %v, want queued: the guess moves nothing", ss, err)
	}

	// The trailer names the slice: jig verifies the commit, and says nothing.
	withTrailer, hook := corrCommit(t, h, "delta/trailer.txt", "e")
	corrSilent(t, "the PostToolUse of a commit with the trailer", hook)
	h.WaitVerifiers()
	if lines := corrVerifyLines(t, fx, withTrailer); len(lines) == 0 {
		t.Errorf("a commit with the trailer was not verified: no journal line for %s", withTrailer)
	}
	if ctx := corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "and the verification?"})); !strings.Contains(ctx, "verified "+withTrailer[:12]+" for slice e") || strings.Contains(ctx, "names no slice") {
		t.Errorf("prompt context = %q, want the verification of %s and no note that it names no slice", ctx, withTrailer)
	}

	// The project defaults to unattended: the first Stop is refused for the
	// commit that names no slice, once.
	corrMarkUnattended(t, fx)
	stop := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if stop.Decision != "block" || !strings.Contains(stop.Reason, "this session is unattended") || !strings.Contains(stop.Reason, "commit "+first[:12]+" on "+fx.Ticket+" names no slice") {
		t.Fatalf("Stop = %+v, want a refusal for the commit that names no slice", stop)
	}
	if refs := corrDeliveredSlice(t, env, "stop-block"); len(refs) != 1 || refs[0] != "slice:"+first[:12] {
		t.Errorf("stop-block refs = %v, want slice:%s once", refs, first[:12])
	}
	corrSilent(t, "the Stop that is continuing for it", h.Hook("Stop", map[string]any{"stop_hook_active": true}))
	corrSilent(t, "the Stop after it", h.Hook("Stop", map[string]any{"stop_hook_active": false}))

	// A new commit is a new fact, refused for once, and only for it.
	second, hook := corrCommit(t, h, "delta/second.txt", "")
	if ctx := corrContext(t, hook); !strings.Contains(ctx, "commit "+second[:12]+" on") {
		t.Errorf("PostToolUse context = %q, want the note of %s", ctx, second)
	}
	stop = decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if stop.Decision != "block" || !strings.Contains(stop.Reason, "commit "+second[:12]+" on") || strings.Contains(stop.Reason, first[:12]) {
		t.Errorf("Stop for the new commit = %+v, want a refusal for %s alone", stop, second)
	}
	corrSilent(t, "the Stop after the new one", h.Hook("Stop", map[string]any{"stop_hook_active": false}))

	// Said to jig verify before the Stop, a commit is no longer held against
	// the session: the third is verified for slice e, and the Stop that
	// follows has nothing to refuse for.
	third, hook := corrCommit(t, h, "delta/third.txt", "")
	if ctx := corrContext(t, hook); !strings.Contains(ctx, "commit "+third[:12]+" on") {
		t.Errorf("PostToolUse context = %q, want the note of %s", ctx, third)
	}
	if res := h.Jig("verify", fx.Ticket, third, "--slice", "e", "--wait"); res.Code != 0 {
		t.Fatalf("jig verify --wait exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	stop = decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if stop.Decision != "" || stop.Reason != "" || strings.Contains(stop.SystemMessage, "names no slice") {
		t.Errorf("Stop after the commit was verified for its slice = %+v, want no refusal and no note that it names no slice", stop)
	}
	if got := len(corrSliceLines(t, fx)); got != 3 {
		t.Errorf("slice-missing lines = %d, want 3, one for each commit without the trailer", got)
	}
}
