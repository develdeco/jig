package e2e

// TestDigesterFlow is the demo of the attached design: a ticket goes from a
// brief to a recorded pull request with no coding session started by jig.
// Everything a harness does is played by the fake harness (hook payloads,
// commits in the person's clone, the push); everything jig does is the real
// binary, installed the way a person installs it (jig setup), driven by the
// hooks and by the verbs an agent runs. Run it with -v to read it as a
// transcript: each jig command is logged with the lines of its output that
// matter.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// productTreeStatus returns `git status --porcelain` for the product repo
// itself, the jig checkout under test.
func productTreeStatus(t *testing.T) string {
	t.Helper()
	out, err := gitx.Run(repoRoot, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status --porcelain: %v", err)
	}
	return strings.TrimSpace(out)
}

func hasSlice(slices []store.Slice, id string) bool {
	for _, s := range slices {
		if s.ID == id {
			return true
		}
	}
	return false
}

// flow narrates a test as a transcript: step names a stage, run runs a jig
// command and logs it with the lines of its output that matter.
type flow struct {
	t *testing.T
}

// step logs the stage the flow is at.
func (f flow) step(format string, args ...any) {
	f.t.Helper()
	f.t.Logf("")
	f.t.Logf("== "+format, args...)
}

// say logs a line of the transcript that is not a command.
func (f flow) say(format string, args ...any) {
	f.t.Helper()
	f.t.Logf("   "+format, args...)
}

// maxShown is how many lines of one command's output the transcript shows.
const maxShown = 14

// jig runs jig as the harness h does (from its clone, as its session) and
// logs the command and its output.
func (f flow) jig(h *fakeHarness, args ...string) jigResult {
	f.t.Helper()
	res := h.Jig(args...)
	f.show(h.Session, res, args...)
	return res
}

// show logs a jig command that was run, and what it printed.
func (f flow) show(who string, res jigResult, args ...string) {
	f.t.Helper()
	f.t.Logf("$ [%s] jig %s    (exit %d)", who, strings.Join(args, " "), res.Code)
	lines := strings.Split(strings.TrimRight(res.Stdout+res.Stderr, "\n"), "\n")
	for i, l := range lines {
		if i == maxShown {
			f.t.Logf("    ... %d more lines", len(lines)-maxShown)
			break
		}
		if strings.TrimSpace(l) != "" {
			f.t.Logf("    %s", l)
		}
	}
}

// hook logs a hook that fired and what it answered.
func (f flow) hook(who, event string, res jigResult) {
	f.t.Helper()
	answer := strings.TrimSpace(res.Stdout)
	if answer == "" {
		answer = "(silent)"
	}
	if len(answer) > 400 {
		answer = answer[:400] + "..."
	}
	f.t.Logf("<- [%s] hook %s    (exit %d) %s", who, event, res.Code, answer)
}

// mustRun runs a jig command as h and fails the test unless it exits 0.
func (f flow) mustRun(h *fakeHarness, args ...string) jigResult {
	f.t.Helper()
	res := f.jig(h, args...)
	if res.Code != 0 {
		f.t.Fatalf("jig %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), res.Code, res.Stdout, res.Stderr)
	}
	return res
}

// flowEvent is a line of `jig events`: where it came from and what it was.
type flowEvent struct {
	Src     string `json:"src"`
	Event   string `json:"event"`
	Slice   string `json:"slice"`
	Outcome string `json:"outcome"`
	Commit  string `json:"commit"`
}

// TestDigesterFlow: the fixture ticket JIG-1 is taken from validation to a
// recorded pull request by a harness that jig does not start.
//
//  1. jig is installed for the machine (hooks, git hook) with jig setup.
//  2. The brief is validated, which approves it.
//  3. The branch to work on is the one jig next names.
//  4. The session starts: its hook tells it the ticket.
//  5. It claims the first slice, unattended.
//  6. It commits slice a. Git's own hook starts a verification of the commit;
//     the PostToolUse hook that sees the same commit starts nothing more.
//  7. While the verification runs, the unattended session is not let stop.
//  8. It commits slice b, which fails its oracle the first time, and slice d.
//  9. Slice c asks the person a question and waits; the person answers and the
//     session is told at its next prompt; slice c is committed.
//  10. Every verification records its outcome, and the session hears them.
//  11. With every slice green, jig next names the gate and the session opens
//     round 1.
//  12. A reviewer in a session of its own reads the round and reports it clean.
//  13. The body of the pull request is rendered, a person approves, the
//     unattended session is told to publish, the harness pushes, and the
//     pull request it opened is recorded.
//  14. jig events tells it all in order.
//  15. The bookkeeping a publish leaves is there, and nothing of the old
//     dispatch: no slice.json anywhere, no jig run.
func TestDigesterFlow(t *testing.T) {
	t.Parallel()
	f := flow{t}
	productBefore := productTreeStatus(t)
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withGitGlobal(t)
	// The gate brings up every env class a slice names and reuses a pass only
	// when exactly those were up, so slice d does not name one here.
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-flow")
	ticket := fx.Ticket
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	f.step("1. jig is set up for this machine: the Claude Code hooks and git's global hook")
	setup := runJig(t, env, fx.RepoDir, "setup", "--global", "--hooks", "--githook", "--harness", "claude", "--yes")
	f.show("person", setup, "setup", "--global", "--hooks", "--githook", "--harness", "claude", "--yes")
	if setup.Code != 0 {
		t.Fatalf("jig setup exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", setup.Code, setup.Stdout, setup.Stderr)
	}
	hooksPath, err := gitx.RunReplaceEnv(fx.Dir, env.environ(), "config", "--global", "core.hooksPath")
	if err != nil || hooksPath == "" {
		t.Fatalf("git config --global core.hooksPath = %q, %v, want jig's hook directory", hooksPath, err)
	}

	f.step("2. the brief is validated, and approved with it")
	f.mustRun(h, "validate", ticket)
	approval, ok, err := st.ReadApproval(ticket)
	if err != nil || !ok || approval.BriefSHA256 == "" {
		t.Fatalf("approval = %+v, %v, %v, want the brief's hash recorded", approval, ok, err)
	}

	f.step("3. the branch to work on is the one jig next names")
	next := f.mustRun(h, "next", ticket, "--json")
	var named struct {
		Branch string `json:"branch"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(next.Stdout), &named); err != nil || named.Branch == "" {
		t.Fatalf("jig next --json = %q (%v), want a branch", next.Stdout, err)
	}
	branch := named.Branch
	h.Branch(branch)

	f.step("4. the session starts: its hook runs the command jig put in settings.json")
	startCmd := installedHookCommand(t, claudeSettings(env), "SessionStart")
	start := runHookCommand(t, env, fx.RepoDir, startCmd, h.Payload("SessionStart", map[string]any{"source": "startup", "model": "claude-test"}))
	f.hook(h.Session, "SessionStart", start)
	context := decodeHook(t, start).Specific.Context
	if !strings.Contains(context, ticket) || !strings.Contains(context, "session: "+h.Session) {
		t.Fatalf("SessionStart context = %q, want it to name %s and the session", context, ticket)
	}

	f.step("5. the session claims slice a, unattended: nobody is watching")
	claim := f.mustRun(h, "claim", ticket, "--slice", "a", "--unattended")
	if !strings.Contains(claim.Stdout, "result: claimed") {
		t.Fatalf("jig claim output lacks the claim:\n%s", claim.Stdout)
	}

	f.step("6. the session commits slice a; git's global hook starts a detached verification of the commit")
	verifySlots <- struct{}{}
	release := sync.OnceFunc(func() { <-verifySlots })
	defer release()
	var spawnedShas []string
	// commit makes the commit of a slice's attempt and waits for the
	// verification git's hook started for it; between, when given, runs while
	// that verification is still running.
	commit := func(slice string, attempt int, between func(sha string)) string {
		t.Helper()
		sha := h.CommitScenario(slice, attempt)
		f.say("git commit: slice %s, attempt %d -> %s", slice, attempt, sha[:12])
		waitFor(t, 30*time.Second, "the verification git's hook started", func() bool { return spawnedFor(readLedger(t, env), sha) == 1 })
		if between != nil {
			between(sha)
		}
		// The harness fires PostToolUse for the commit its Bash tool made. The
		// verification is already started, so this hook starts nothing.
		post := h.Hook("PostToolUse", map[string]any{
			"tool_name": "Bash",
			"command":   "git commit -m '" + slice + "' -m 'Jig-Slice: " + slice + "'",
			"tool_response": map[string]any{
				"stdout": "[" + branch + " " + sha[:7] + "] " + slice, "stderr": "", "interrupted": false,
			},
		})
		f.hook(h.Session, "PostToolUse", post)
		if post.Code != 0 {
			t.Fatalf("PostToolUse hook exit = %d, want 0", post.Code)
		}
		spawnedShas = append(spawnedShas, sha)
		return sha
	}

	shaA := commit("a", 1, func(sha string) {
		f.step("7. the verification is still running: the unattended session is not let stop")
		stop := h.Hook("Stop", map[string]any{"stop_hook_active": false})
		f.hook(h.Session, "Stop", stop)
		blocked := decodeHook(t, stop)
		if blocked.Decision != "block" || !strings.Contains(blocked.Reason, "verification of "+sha[:12]+" for slice a is still running") ||
			!strings.Contains(blocked.Reason, "unattended") {
			t.Fatalf("Stop while a verification runs = %+v, want a block naming the verification of %s", blocked, sha[:12])
		}
		// Told once for that fact: a session that stops again because of it,
		// with nothing new to say, is let go.
		again := h.Hook("Stop", map[string]any{"stop_hook_active": true})
		f.hook(h.Session, "Stop (continuing)", again)
		if decodeHook(t, again).Decision == "block" {
			t.Errorf("Stop with stop_hook_active held the session back a second time for the same fact:\n%s", again.Stdout)
		}
	})

	f.step("8. the session goes on: slice b fails its oracle once and passes the second time, and slice d")
	shaB1 := commit("b", 1, nil)
	shaB2 := commit("b", 2, nil)
	shaD := commit("d", 1, nil)

	f.step("9. slice c needs the person's word on the greeting: the session asks, and waits a moment")
	ask := f.jig(h, "ask", ticket, "--slice", "c", "--wait", "--timeout", "1s", "Formal or casual greeting?")
	if ask.Code != 2 || !strings.Contains(ask.Stdout, "q-001") {
		t.Fatalf("jig ask --wait that runs out: exit = %d, want 2 naming q-001\nstdout:\n%s\nstderr:\n%s", ask.Code, ask.Stdout, ask.Stderr)
	}
	f.say("the person answers, from wherever they are")
	f.mustRun(h, "answer", ticket, "q-001", "--as", "person", "Casual.")
	prompt := h.Hook("UserPromptSubmit", map[string]any{"prompt": "carry on"})
	f.hook(h.Session, "UserPromptSubmit", prompt)
	if got := decodeHook(t, prompt).Specific.Context; !strings.Contains(got, "Casual.") {
		t.Errorf("the next prompt's context = %q, want it to hand the session the answer", got)
	}
	shaC := commit("c", 2, nil)

	f.step("10. every verification records its outcome, and the session hears them")
	for _, c := range []struct{ slice, sha string }{{"a", shaA}, {"b", shaB2}, {"c", shaC}, {"d", shaD}} {
		l := waitJournal(t, fx, ticket, verifyWait, func(l journal.Line) bool {
			return l.Event == journal.EventVerified && l.Slice == c.slice && l.Commit == c.sha
		})
		f.say("verified: slice %s at %s", l.Slice, l.Commit[:12])
	}
	failed := waitJournal(t, fx, ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventOracle && l.Outcome == "fail" && l.Slice == "b" && l.Commit == shaB1
	})
	f.say("failed: slice b at %s: %s", failed.Commit[:12], firstLine(failed.Summary))
	h.WaitVerifiers()
	release()
	ledger := readLedger(t, env)
	for _, sha := range spawnedShas {
		if n := spawnedFor(ledger, sha); n != 1 {
			t.Errorf("%d verifications were started for %s, want 1: the hooks that see a commit after the first start nothing", n, sha[:12])
		}
	}
	// The outcomes reach the session at its next prompt, once; with nothing
	// left unsaid, a Stop has nothing to add.
	heard := h.Hook("UserPromptSubmit", map[string]any{"prompt": "and now?"})
	f.hook(h.Session, "UserPromptSubmit", heard)
	if got := decodeHook(t, heard).Specific.Context; !strings.Contains(got, "verified "+shaC[:12]+" for slice c") {
		t.Errorf("the prompt after the last verification has the context %q, want the outcome for slice c", got)
	}
	if res := h.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("Stop with every slice verified and heard = exit %d, %q, want silence", res.Code, res.Stdout)
	}

	f.step("11. every slice is green: jig next names the gate, and the session opens a round")
	nextText := f.mustRun(h, "next", ticket)
	if !strings.Contains(nextText.Stdout, "jig gate "+ticket) {
		t.Fatalf("jig next with every slice green does not name the gate:\n%s", nextText.Stdout)
	}
	gate := f.mustRun(h, "gate", ticket)
	if !strings.Contains(gate.Stdout, "round: 1") || !strings.Contains(gate.Stdout, "head: "+shaC) && !strings.Contains(gate.Stdout, "head: "+shaD) {
		// The head is the last commit of the branch, whichever slice made it.
		f.say("(the round's head is the branch's tip)")
	}
	head := h.git("rev-parse", "HEAD")
	if !strings.Contains(gate.Stdout, "head: "+head) {
		t.Fatalf("jig gate does not name the branch's head %s:\n%s", head, gate.Stdout)
	}

	f.step("12. a reviewer in a session of its own reads the round and reports it clean")
	reviewer := newHarness(t, fx, env, "r2")
	f.mustRun(reviewer, "review", "context", ticket)
	reviewer.Bash("git log --oneline -3")
	done := f.mustRun(reviewer, "review", "done", ticket, "--result", reviewResult(t, st, ticket, 1, false))
	for _, want := range []string{"verdict: clean", "independent: true"} {
		if !strings.Contains(done.Stdout, want) {
			t.Fatalf("jig review done output lacks %q:\n%s", want, done.Stdout)
		}
	}
	if res := h.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("Stop with a clean round no person approved = exit %d, %q, want silence: publishing waits for the person", res.Code, res.Stdout)
	}

	f.step("13. the pull request: its body is rendered, a person approves, the harness pushes and opens it, jig records it")
	env, ghLog := githubRemote(t, fx, env)
	h.env = env
	body := f.mustRun(h, "publish", ticket, "--body")
	if !strings.Contains(body.Stdout, "head: "+head) {
		t.Fatalf("jig publish --body does not name the reviewed head %s:\n%s", head, body.Stdout)
	}
	// What the person's approval writes (at a terminal, or from the TUI's p
	// key; a test has neither): a person's, of the head the round reviewed.
	if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPublishApproved, Commit: head, Branch: branch, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	f.say("a person approves publishing %s", head[:12])
	stop := h.Hook("Stop", map[string]any{"stop_hook_active": false})
	f.hook(h.Session, "Stop", stop)
	blocked := decodeHook(t, stop)
	if blocked.Decision != "block" || !strings.Contains(blocked.Reason, "jig publish "+ticket) || !strings.Contains(blocked.Reason, head[:12]) {
		t.Fatalf("Stop with an approved, unpublished ticket = %+v, want a block that names `jig publish %s`", blocked, ticket)
	}
	// Told once for that fact, here too: the session that stops again with the
	// approval already given it has nothing more to be told.
	if again := h.Hook("Stop", map[string]any{"stop_hook_active": true}); again.Code != 0 || again.Stdout != "" {
		t.Errorf("Stop with stop_hook_active after the approval was given = exit %d, %q, want silence", again.Code, again.Stdout)
	}
	h.Push(branch)
	const prURL = "https://github.com/acme/fixture-repo/pull/7"
	prEnv := env.with("GH_STUB_HEAD_REF="+branch, "GH_STUB_HEAD_OID="+head)
	h.env = prEnv
	rec := f.mustRun(h, "publish", ticket, "--pr", prURL)
	h.env = env
	if !strings.Contains(rec.Stdout, "verified: yes") {
		t.Fatalf("jig publish --pr did not verify the pull request against the branch:\n%s", rec.Stdout)
	}
	if res := h.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("Stop once the ticket is published = exit %d, %q, want silence", res.Code, res.Stdout)
	}
	if got := originRefOrEmpty(t, fx.RepoRemote, "refs/heads/"+branch); got != head {
		t.Errorf("origin's %s = %s, want the reviewed head %s", branch, got, head)
	}
	for _, c := range ghCalls(t, ghLog) {
		if strings.HasPrefix(c, "pr create") || strings.HasPrefix(c, "pr edit") {
			t.Errorf("jig called gh %q: the harness opens the pull request, jig records it", c)
		}
	}

	f.step("14. jig events tells the whole story, in order")
	events := h.Jig("events", ticket)
	if events.Code != 0 {
		t.Fatalf("jig events exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", events.Code, events.Stdout, events.Stderr)
	}
	f.t.Logf("$ [%s] jig events %s    (exit 0): the journal of the ticket and the machine ledger as one stream", h.Session, ticket)
	var seen []string
	var got []flowEvent
	sc := bufio.NewScanner(strings.NewReader(events.Stdout))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var ev flowEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("jig events printed a line that is not JSON: %q (%v)", sc.Text(), err)
		}
		got = append(got, ev)
		seen = append(seen, ev.Event)
		f.t.Logf("    %-8s %-20s %-3s %s", ev.Src, ev.Event, ev.Slice, ev.Outcome)
	}
	want := []string{
		journal.EventClaim, journal.EventVerifyPending, journal.EventOracle, journal.EventVerified,
		journal.EventGateOpen, journal.EventGateClean, journal.EventPrBody, journal.EventPublishApproved,
		journal.EventPr, journal.EventPublishDone,
	}
	if !subsequence(seen, want) {
		t.Errorf("jig events has the events\n  %s\nwant these among them, in this order\n  %s", strings.Join(seen, " "), strings.Join(want, " "))
	}
	sources := map[string]bool{}
	for _, ev := range got {
		sources[ev.Src] = true
	}
	if !sources["journal"] || !sources["ledger"] {
		t.Errorf("jig events came from %v, want the ticket's journal and the machine ledger both", keysOf(sources))
	}

	f.step("15. the bookkeeping a publish leaves, and nothing of the old dispatch")
	ticketDir := st.TicketDir(ticket)
	for _, ws := range []string{"alpha", "beta"} {
		if data := readFileOrFatal(t, filepath.Join(ticketDir, "changelog", ws+".md")); strings.TrimSpace(string(data)) == "" {
			t.Errorf("the changelog of workspace %s is empty", ws)
		}
	}
	ledgerMD := string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "ledger.md")))
	if !strings.Contains(ledgerMD, ticket) || !strings.Contains(ledgerMD, "q-001") || !strings.Contains(ledgerMD, "Casual.") {
		t.Errorf("ledger.md has no entry for %s with the answer to q-001:\n%s", ticket, ledgerMD)
	}
	contractIndex := string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "platform", "contract-index.md")))
	if !strings.Contains(contractIndex, ticket) || !strings.Contains(contractIndex, "alpha") || !strings.Contains(contractIndex, "beta") {
		t.Errorf("platform/contract-index.md has no entry for %s naming its workspaces:\n%s", ticket, contractIndex)
	}
	if body := readFileOrFatal(t, filepath.Join(ticketDir, "pr", "fixture-repo.md")); strings.TrimSpace(string(body)) == "" {
		t.Error("the pull request body is empty")
	}
	var sliceJSON []string
	for _, root := range []string{fx.Dir, env.home} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".slice.json") {
				sliceJSON = append(sliceJSON, path)
			}
			return nil
		})
	}
	if len(sliceJSON) != 0 {
		t.Errorf("a *.slice.json was written, but jig dispatches no slice: %v", sliceJSON)
	}
	run := runJig(t, env, fx.RepoDir, "run", ticket)
	f.show("person", run, "run", ticket)
	if run.Code == 0 || !strings.Contains(run.Stdout, "unknown command") {
		t.Errorf("jig run: exit %d, want it refused as an unknown command\n%s", run.Code, run.Stdout)
	}
	if after := productTreeStatus(t); after != productBefore {
		t.Errorf("the flow changed the product repo's working tree:\nbefore:\n%s\nafter:\n%s", productBefore, after)
	}
}

// subsequence reports whether want appears in got in order, with anything
// between.
func subsequence(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}
