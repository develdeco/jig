package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// phaseOut is the phase block of `jig next <ticket> --json`.
type phaseOut struct {
	Phase struct {
		Name   string `json:"name"`
		Ready  bool   `json:"ready"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
		Prompt string `json:"prompt"`
		Phases []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Text  string `json:"text"`
		} `json:"phases"`
		For *struct {
			Phase   string `json:"phase"`
			Ready   bool   `json:"ready"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
			Warning string `json:"warning"`
		} `json:"for"`
	} `json:"phase"`
}

// phaseStates is the state of each of the four phases, in order, as one
// string: "dcpp" is the brief done, the build current, the rest pending.
func (p phaseOut) states() string {
	var out string
	for _, ph := range p.Phase.Phases {
		out += ph.State[:1]
	}
	return out
}

// TestNextNamesPhaseAndPrompt: a ticket goes from a drafted brief through an
// approved one, a build under way, a build done, a round open and a clean round,
// and at each point `jig next <ticket>` names the phase it is at, whether that
// phase can start, and the prompt that starts it, in text and in JSON.
func TestNextNamesPhaseAndPrompt(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket

	// expect checks both forms of one `jig next` answer.
	expect := func(when, phase string, ready bool, code, prompt, states string) phaseOut {
		t.Helper()
		out := nextJSON[phaseOut](t, env, fx.RepoDir, T)
		p := out.Phase
		if p.Name != phase || p.Ready != ready || p.Code != code || p.Prompt != prompt || out.states() != states {
			t.Errorf("%s: phase=%s ready=%v code=%q prompt=%q states=%s, want %s %v %q %q %s (reason %q)",
				when, p.Name, p.Ready, p.Code, p.Prompt, out.states(), phase, ready, code, prompt, states, p.Reason)
		}
		if !ready && p.Reason == "" {
			t.Errorf("%s: not ready, and no reason says why", when)
		}
		text := runJig(t, env, fx.RepoDir, "next", T)
		yes := map[bool]string{true: "yes", false: "no"}[ready]
		for _, want := range []string{"phase: " + phase + "\n", "ready: " + yes + "\n", "prompt: " + prompt + "\n"} {
			if !strings.Contains(text.Stdout, want) {
				t.Errorf("%s: jig next %s lacks %q:\n%s", when, T, want, text.Stdout)
			}
		}
		if !ready && !strings.Contains(text.Stdout, "code: "+code+"\n") {
			t.Errorf("%s: jig next %s lacks the code %s:\n%s", when, T, code, text.Stdout)
		}
		return out
	}
	asked := func(phase string) phaseOut {
		t.Helper()
		return nextJSON[phaseOut](t, env, fx.RepoDir, T, "--for", phase)
	}

	// The brief is drafted and nobody approved it: planning is what the ticket
	// is at, and the build waits for the approval.
	expect("a drafted brief", "brief", true, "", "Plan "+T, "cppp")
	if f := asked("build").Phase.For; f == nil || f.Ready || f.Code != "PHASE_BRIEF_NOT_APPROVED" || !strings.Contains(f.Reason, "is not approved") {
		t.Errorf("--for build on a drafted brief: %+v, want PHASE_BRIEF_NOT_APPROVED", f)
	}
	if r := runJig(t, env, fx.RepoDir, "next", T, "--for", "build"); !strings.Contains(r.Stdout, "for_code: PHASE_BRIEF_NOT_APPROVED\n") || !strings.Contains(r.Stdout, "for_ready: no\n") {
		t.Errorf("--for build in text:\n%s", r.Stdout)
	}

	// The brief is approved.
	if r := runJig(t, env, fx.RepoDir, "validate", T); r.Code != 0 {
		t.Fatalf("jig validate exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	expect("an approved brief", "build", true, "", "Build "+T, "dcpp")
	if f := asked("brief").Phase.For; f == nil || !f.Ready || !strings.HasPrefix(f.Warning, "brief is done (approved by ") || !strings.HasSuffix(f.Warning, "; prompt: Build "+T) {
		t.Errorf("--for brief once approved: %+v, want a warning with the prompt that moves on", f)
	}

	// A slice is claimed: still the build, still ready for the other slices.
	if r := runJig(t, env.with("JIG_SESSION=s-claim"), fx.RepoDir, "claim", T, "--slice", "a"); r.Code != 0 {
		t.Fatalf("jig claim exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	expect("a build under way", "build", true, "", "Build "+T, "dcpp")
	if f := asked("review").Phase.For; f == nil || f.Ready || f.Code != "PHASE_NOT_BUILT" {
		t.Errorf("--for review while slices are short of green: %+v, want PHASE_NOT_BUILT", f)
	}

	// Every slice is green, each verified by jig.
	b := buildTicketOn(t, fx, env, true)
	expect("a build done", "review", true, "", "Review "+T, "ddcp")
	if f := asked("build").Phase.For; f == nil || !f.Ready || f.Code != "" || !strings.HasPrefix(f.Warning, "build is done (") || !strings.HasSuffix(f.Warning, "; prompt: Review "+T) {
		t.Errorf("--for build once built: %+v, want a warning, not a refusal", f)
	}
	if f := asked("publish").Phase.For; f == nil || f.Ready || f.Code != "PHASE_NOT_REVIEWED" {
		t.Errorf("--for publish before a review: %+v, want PHASE_NOT_REVIEWED", f)
	}

	// A round is open.
	b.openRound(t)
	open := expect("a round open", "review", false, "PHASE_ROUND_OPEN", "Review "+T, "ddcp")
	if !strings.Contains(open.Phase.Reason, "round 1") {
		t.Errorf("the open round's reason = %q, want it to name round 1", open.Phase.Reason)
	}

	// The round closed clean.
	b.reviewedClean(t)
	expect("a clean round", "publish", true, "", "Publish "+T, "dddc")
}

// approveBriefOf records an approval of the brief of ticket as it is now.
func approveBriefOf(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	sum, err := st.BriefSHA256(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteApproval(ticket, store.Approval{By: "P", At: "2026-10-25T00:00:00Z", BriefSHA256: sum, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
}

// putTicketFile writes one file of a ticket's folder in the store.
func putTicketFile(t *testing.T, st *store.Store, ticket, name, content string) {
	t.Helper()
	path := filepath.Join(st.TicketDir(ticket), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, path, []byte(content))
}

// TestHintsMatchRefusals: whenever a claim is refused or warned, or a gate is
// refused for a slice that is not green, the refusal's help and the hints `jig
// status`, `jig here` and `jig next` print name the same prompt, since all of
// them read the one derivation (internal/phase).
func TestHintsMatchRefusals(t *testing.T) {
	t.Parallel()

	// named checks that every output names prompt, after "ask: " (the hints
	// and the refusals) or as `jig next`'s prompt key.
	named := func(t *testing.T, what, prompt string, outs map[string]string) {
		t.Helper()
		for name, out := range outs {
			if !strings.Contains(out, "ask: "+prompt) && !strings.Contains(out, "prompt: "+prompt+"\n") {
				t.Errorf("%s: %s does not name the prompt %q:\n%s", what, name, prompt, out)
			}
		}
	}
	// hints runs the three commands that advise the next step.
	hints := func(t *testing.T, env jigEnv, cwd, ticket string) (status, here, next string) {
		t.Helper()
		return runJig(t, env, cwd, "status", ticket).Stdout, runJig(t, env, cwd, "here", ticket).Stdout, runJig(t, env, cwd, "next", ticket).Stdout
	}

	t.Run("a brief nobody approved", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		T := fx.Ticket
		const prompt = "Plan JIG-1"

		refused := runJig(t, env, fx.RepoDir, "claim", T, "--slice", "a", "--unattended")
		if refused.Code != 1 || !strings.Contains(refused.Stdout, "code: CLAIM_NOT_APPROVED") {
			t.Fatalf("unattended claim: exit %d, want CLAIM_NOT_APPROVED\n%s", refused.Code, refused.Stdout)
		}
		status, here, next := hints(t, env, fx.RepoDir, T)
		named(t, "claim refused", prompt, map[string]string{"the refusal": refused.Stdout, "status": status, "here": here, "next": next})
		if !strings.Contains(refused.Stdout, prompt+" (approve its brief)") {
			t.Errorf("the refusal does not say what the prompt does:\n%s", refused.Stdout)
		}

		// A person watching is warned, and the claim goes through.
		warned := runJig(t, env, fx.RepoDir, "claim", T, "--slice", "a")
		if warned.Code != 0 || !strings.Contains(warned.Stdout, "warnings[1]{code,message,help}:") {
			t.Fatalf("attended claim: exit %d, want a warning\n%s", warned.Code, warned.Stdout)
		}
		named(t, "claim warned", prompt, map[string]string{"the warning": warned.Stdout})

		// With the work under way the ticket is past the brief, whatever the
		// warning goes on saying: the hints move on to the build.
		status, _, _ = hints(t, env, fx.RepoDir, T)
		named(t, "after the person went ahead", "Build JIG-1", map[string]string{"status": status})
	})

	t.Run("a brief that changed after its approval", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		T := fx.Ticket
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatal(err)
		}
		approveBriefOf(t, st, T)
		putTicketFile(t, st, T, "brief.md", "# Fixture ticket brief\n\n## Goal\n\nChanged after it was approved.\n")

		refused := runJig(t, env, fx.RepoDir, "claim", T, "--slice", "a", "--unattended")
		if refused.Code != 1 || !strings.Contains(refused.Stdout, "changed after it was approved") {
			t.Fatalf("claim after the change: exit %d\n%s", refused.Code, refused.Stdout)
		}
		status, here, next := hints(t, env, fx.RepoDir, T)
		named(t, "claim refused", "Plan JIG-1", map[string]string{"the refusal": refused.Stdout, "status": status, "here": here, "next": next})
	})

	t.Run("a slice that is not green", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		T := fx.Ticket
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatal(err)
		}
		approveBriefOf(t, st, T)

		for _, args := range [][]string{{"gate", T}, {"review", "context", T}} {
			name := "jig " + strings.Join(args, " ")
			refused := runJig(t, env, fx.RepoDir, args...)
			if refused.Code != 1 || !strings.Contains(refused.Stdout, "code: GATE_NOT_GREEN") {
				t.Fatalf("%s: exit %d, want GATE_NOT_GREEN\n%s", name, refused.Code, refused.Stdout)
			}
			status, here, next := hints(t, env, fx.RepoDir, T)
			named(t, name+" refused", "Build JIG-1", map[string]string{"the refusal": refused.Stdout, "status": status, "here": here, "next": next})
		}
	})

	t.Run("a blocker that has not published", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatal(err)
		}
		approveBriefOf(t, st, fx.Ticket)
		putTicketFile(t, st, "JIG-2", "ticket.yaml", "schema_version: 1\ntitle: Second\nblocked_by:\n  - ticket: JIG-1\n    kind: merged\n")
		putTicketFile(t, st, "JIG-2", "brief.md", "# Second\n\n## Goal\n\nDo it.\n")
		putTicketFile(t, st, "JIG-2", "slices.yaml", "slices:\n  - id: x\n    workspace: alpha\n    goal: Do it.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")
		approveBriefOf(t, st, "JIG-2")

		// The blocker is a ticket at the build, so building it is what makes
		// JIG-2 ready.
		const prompt = "Build JIG-1"
		refused := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended")
		if refused.Code != 1 || !strings.Contains(refused.Stdout, "code: CLAIM_BLOCKED_MERGED") {
			t.Fatalf("claim of a blocked ticket: exit %d\n%s", refused.Code, refused.Stdout)
		}
		status, here, next := hints(t, env, fx.RepoDir, "JIG-2")
		named(t, "claim refused", prompt, map[string]string{"the refusal": refused.Stdout, "status": status, "here": here, "next": next})
		if !strings.Contains(next, "code: PHASE_BLOCKED\n") || !strings.Contains(next, "ready: no\n") {
			t.Errorf("next does not say the build is blocked:\n%s", next)
		}
	})
}

// TestATicketFolderOfLocksIsNoTicket: a ticket folder holding nothing but
// jig's lock files (what a store reset or checkout leaves of a ticket it
// removed, since the store ignores them) is not a ticket at the brief phase.
// `jig next` refuses it and points at `jig ticket new`, as for a ticket with no
// folder, so a session asked to plan it mints the ticket instead of drafting a
// brief into a folder that has no record.
func TestATicketFolderOfLocksIsNoTicket(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := filepath.Join(fx.StoreDir, "tickets", "JIG-404")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ticket.yaml.lock", "journal.ndjson.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := runJig(t, env, fx.RepoDir, "next", "JIG-404")
	if r.Code != 2 || !strings.Contains(r.Stdout, "not found in the store") || !strings.Contains(r.Stdout, "jig ticket new") {
		t.Errorf("jig next on a folder of locks: exit %d, want 2 saying it is not found and to mint it\n%s", r.Code, r.Stdout)
	}
	// Nor does the store's listing show it.
	if r := runJig(t, env, fx.RepoDir, "next"); r.Code != 0 || strings.Contains(r.Stdout, "JIG-404") || !strings.Contains(r.Stdout, fx.Ticket) {
		t.Errorf("jig next lists a folder of locks, or not the fixture's ticket: exit %d\n%s", r.Code, r.Stdout)
	}
}
