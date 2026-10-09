package verifydeliver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/session"
)

// gateCleanReviewerRound drives one real, reviewer-sourced gate round to a
// clean verdict: unlike gateToClean's scripted rounds, this is what actually
// records a ReviewedSHA in report.yaml, the head publish picks the build's
// recordings for and checkReviewedHead keys off of - a scripted round never
// sets it (fakeGateSource.Round never sets Round.Review), so a test that
// wants to exercise the pick and attach path needs this instead.
func gateCleanReviewerRound(t *testing.T, fx *fixture.Fixture, d Deps) {
	t.Helper()
	driveBuild(t, fx, "rung-a")
	reviewer := stubBackend{run: func(sd session.Dispatch) error {
		writeMustReviewResult(t, sd)
		return nil
	}}
	report, err := Gate(d, NewReviewerGateSource(reviewer), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("verdict = %q, want clean", report.Verdict)
	}
}

// findGhCall returns the first logged call (never a --help probe) whose argv
// (after the gh binary itself) has prefix, or nil.
func findGhCall(calls []ghLogLine, prefix ...string) *ghLogLine {
	for i := range calls {
		argv := calls[i].Argv
		if len(argv) < len(prefix)+1 || hasHelpArg(argv) {
			continue
		}
		match := true
		for j, p := range prefix {
			if argv[j+1] != p {
				match = false
				break
			}
		}
		if match {
			return &calls[i]
		}
	}
	return nil
}

func hasHelpArg(argv []string) bool {
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// sameDir reports whether a and b name the same directory once symlinks are
// resolved. The fake gh logs the working directory os.Getwd gives it, and
// with an explicit environment (repohost.NewWithEnv) the child gets no PWD
// from os/exec, so on macOS it reads /private/var/... where the test's own
// temp path says /var/....
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

func attachedFiles(argv []string) []string {
	var out []string
	for i, a := range argv {
		if a == "--attach" && i+1 < len(argv) {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// TestPublishAttachesPickedMediaOnUpdate: the attach path runs through
// UpdatePRWithMedia when the branch already has an open pull request, with the
// staging directory of the *reviewed* head as gh's own working directory and
// one --attach per picked file, in the order the pull request shows them.
func TestPublishAttachesPickedMediaOnUpdate(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	branch := ticketBranch(pf.fx.Ticket)
	run(t, buildLeaseDir(t, pf.fx), "push", "origin", branch)
	pf.logFile, _ = useGithubHost(t, &pf.d, openPullOf(t, branch), "")

	report, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !report.PRUpdated["fixture-repo"] {
		t.Fatalf("report.PRUpdated = %v, want the existing pull request updated", report.PRUpdated)
	}

	edit := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "edit", existingPR)
	if edit == nil {
		t.Fatal("no logged pr edit call")
	}
	if dir := pf.picksDir(t); !sameDir(t, edit.Dir, dir) {
		t.Errorf("pr edit ran in %q, want the staging directory for the reviewed head %q", edit.Dir, dir)
	}
	if got, want := attachedFiles(edit.Argv), []string{"rec-1.png", "rec-2.svg", "rec-3.mp4"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pr edit --attach files = %v, want %v", got, want)
	}
}

// TestPublishWarnsAboutAnUnrewrittenMediaReference: once the attach call
// returns, publish reads the pull request's body back and, finding a
// ./<name> reference gh left unrewritten with no upload URL appended for it
// either (an image gh itself did not recognize, despite "Videos" saying it
// would), nothing here can patch it: publish warns, naming the file, not an
// arbitrary line of body prose.
func TestPublishWarnsAboutAnUnrewrittenMediaReference(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	_, addEnv := useGithubHost(t, &pf.d, "", "")
	addEnv("GH_STUB_BODY=## Demo\n\n- ./rec-1.png: still here, unrewritten\n")

	if _, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var warning string
	for _, w := range pf.warnings {
		if strings.Contains(w, "unrewritten") {
			warning = w
		}
	}
	if warning == "" {
		t.Fatalf("warnings = %v, want one about an unrewritten media reference", pf.warnings)
	}
	if !strings.Contains(warning, "rec-1.png") {
		t.Errorf("warning = %q, want it to name rec-1.png", warning)
	}
}

// TestPublishWarnsWhenGhLacksAttachSupport: an installed gh with no --attach
// on either subcommand (GH_STUB_NO_ATTACH) still opens the pull request, with
// no media and a warning naming why - never a silent pull request with
// broken ./<name> links and no explanation.
func TestPublishWarnsWhenGhLacksAttachSupport(t *testing.T) {
	t.Parallel()
	pf := newPicksFixture(t)
	pf.onboardingRecordings(t)
	spy := picksScenario(t, onboardingPick)
	var addEnv func(...string)
	pf.logFile, addEnv = useGithubHost(t, &pf.d, "", "")
	addEnv("GH_STUB_NO_ATTACH=1")

	if _, err := Publish(pf.d, PublishOpts{Ticket: pf.fx.Ticket, Yes: true, Backend: spy}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	create := findGhCall(loggedGhCalls(t, pf.logFile), "pr", "create")
	if create == nil {
		t.Fatal("no logged pr create call")
	}
	if len(attachedFiles(create.Argv)) != 0 {
		t.Errorf("pr create argv %v carries --attach despite no support", create.Argv)
	}
	if !pf.warned("--attach") {
		t.Fatalf("warnings = %v, want one naming the installed gh's lack of --attach support", pf.warnings)
	}
}
