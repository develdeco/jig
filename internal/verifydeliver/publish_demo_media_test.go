package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// demoMediaSpec is one file a test wants recorded as part of a demo: its
// name (demo.yaml's own naming, demo-<n>.<ext>) and its content, from which
// its sha256 and size are computed - never hand-picked, so a test that wants
// a mismatch has to corrupt the file or the record after this, not write a
// wrong value by construction.
type demoMediaSpec struct{ Name, Content string }

// gateCleanReviewerRound drives one real, reviewer-sourced gate round to a
// clean verdict (a plain --no-demo run of NewReviewerGateSource, reused from
// demogate_test.go's own demoStub): unlike gateToClean's scripted rounds,
// this is what actually records a ReviewedSHA in report.yaml, the head
// renderDemoSection and checkReviewedHead both key off of - a scripted round
// never sets it (fakeGateSource.Round never sets Round.Review), so a test
// that wants to exercise the demo/attach path needs this instead.
func gateCleanReviewerRound(t *testing.T, fx *fixture.Fixture, d Deps) {
	t.Helper()
	driveBuild(t, fx, "rung-a")
	stub := &demoStub{t: t}
	report, err := Gate(d, NewReviewerGateSource(stub), GateOpts{Ticket: fx.Ticket, NoDemo: true})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("verdict = %q, want clean", report.Verdict)
	}
}

// recordDemoForTicket writes a recorded demo.yaml for ticket's own
// just-reviewed head (the latest gate round's report.yaml, same as
// renderDemoSection reads), with specs written to disk under the real jig
// home's evidence directory for that head - the state gateDemo would have
// left after verifying, renaming and pruning a session's own result. It
// returns that directory and the DemoFile records written, so a test can
// assert gh ran with this directory as its own working directory and these
// names as its --attach arguments.
func recordDemoForTicket(t *testing.T, d Deps, ticket string, specs []demoMediaSpec) (mediaDir string, files []DemoFile) {
	t.Helper()
	lastRound, err := existingGateRounds(d.Store, ticket)
	if err != nil {
		t.Fatalf("existingGateRounds: %v", err)
	}
	rep := reportYAMLAt(t, d, ticket, lastRound)
	head := rep.ReviewedSHA["fixture-repo"]
	if head == "" {
		t.Fatalf("round %d report.yaml has no reviewed head for fixture-repo", lastRound)
	}
	mediaDir, err = demoMediaDir(d, ticket, head)
	if err != nil {
		t.Fatalf("demoMediaDir: %v", err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	for _, s := range specs {
		if err := os.WriteFile(filepath.Join(mediaDir, s.Name), []byte(s.Content), 0o644); err != nil {
			t.Fatalf("write %s: %v", s.Name, err)
		}
		sum := sha256.Sum256([]byte(s.Content))
		files = append(files, DemoFile{
			Name: s.Name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(s.Content)),
			Caption: "caption for " + s.Name,
		})
	}
	if _, err := writeDemoRecord(d.Store, ticket, lastRound, head, files, "it works", nil); err != nil {
		t.Fatalf("writeDemoRecord: %v", err)
	}
	return mediaDir, files
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
// with an explicit environment (tracker.NewWithEnv) the child gets no PWD
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

// TestPublishAttachesDemoMediaOnCreate: when the shipped head has a recorded
// demo, publish opens the pull request through CreatePRWithMedia, with the
// evidence directory for the *reviewed* head - never the post-squash head
// `head` names by the time this runs - as gh's own working directory, and one
// --attach per verified file. Before the fix this ran gh in a directory that
// does not exist (the post-squash tip's own, never written to), which fails
// before gh even starts; this test's gh call would error on exactly that if
// the regression returned.
func TestPublishAttachesDemoMediaOnCreate(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateCleanReviewerRound(t, fx, d)
	mediaDir, files := recordDemoForTicket(t, d, fx.Ticket, []demoMediaSpec{
		{Name: "demo-1.png", Content: "first file bytes"},
		{Name: "demo-2.mp4", Content: "second file bytes, a bit longer"},
	})
	logFile, _ := useGithubPRs(t, &d, "", "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if recomputed, err := demoMediaDir(d, fx.Ticket, report.Head["fixture-repo"]); err != nil {
		t.Fatalf("demoMediaDir: %v", err)
	} else if recomputed == mediaDir {
		t.Fatalf("test setup: the evidence directory recomputed from the pushed head must not equal the one for the reviewed head, or this test cannot catch the post-squash-head regression")
	}

	calls := loggedGhCalls(t, logFile)
	create := findGhCall(calls, "pr", "create")
	if create == nil {
		t.Fatal("no logged pr create call")
	}
	if !sameDir(t, create.Dir, mediaDir) {
		t.Errorf("pr create ran in %q, want the evidence directory for the reviewed head %q", create.Dir, mediaDir)
	}
	got := attachedFiles(create.Argv)
	if len(got) != len(files) || got[0] != files[0].Name || got[1] != files[1].Name {
		t.Errorf("pr create --attach files = %v, want %s then %s in demo.yaml's own order", got, files[0].Name, files[1].Name)
	}

	// The fake gh rewrites the image's markdown reference in place to its own
	// upload URL (asset 1, the first --attach); the video's bare path is left
	// as it was, so publish reads the upload URL gh appended for it (asset 2)
	// back from the pull request and patches the stored body itself.
	body, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	if err != nil {
		t.Fatalf("read pr body: %v", err)
	}
	if !strings.Contains(string(body), "![caption for demo-1.png](https://github.example/user-attachments/assets/1)") {
		t.Errorf("pr body = %q, want the image reference rewritten to its upload URL", body)
	}
	if !strings.Contains(string(body), "https://github.example/user-attachments/assets/2: caption for demo-2.mp4") {
		t.Errorf("pr body = %q, want the video's bare reference patched to its upload URL", body)
	}
	if strings.Contains(string(body), "./demo-1.png") || strings.Contains(string(body), "./demo-2.mp4") {
		t.Errorf("pr body = %q, want no dead relative path left for either file", body)
	}
}

// TestPublishAttachesDemoMediaOnUpdate: the same attach path runs through
// UpdatePRWithMedia when the branch already has an open pull request.
func TestPublishAttachesDemoMediaOnUpdate(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateCleanReviewerRound(t, fx, d)
	mediaDir, files := recordDemoForTicket(t, d, fx.Ticket, []demoMediaSpec{
		{Name: "demo-1.gif", Content: "a gif's bytes"},
	})
	branch := ticketBranch(fx.Ticket)
	run(t, buildLeaseDir(t, fx), "push", "origin", branch)
	logFile, _ := useGithubPRs(t, &d, openPullOf(t, branch), "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !report.PRUpdated["fixture-repo"] {
		t.Fatalf("report.PRUpdated = %v, want the existing pull request updated", report.PRUpdated)
	}

	calls := loggedGhCalls(t, logFile)
	edit := findGhCall(calls, "pr", "edit", existingPR)
	if edit == nil {
		t.Fatal("no logged pr edit call")
	}
	if !sameDir(t, edit.Dir, mediaDir) {
		t.Errorf("pr edit ran in %q, want the evidence directory for the reviewed head %q", edit.Dir, mediaDir)
	}
	if got := attachedFiles(edit.Argv); len(got) != 1 || got[0] != files[0].Name {
		t.Errorf("pr edit --attach files = %v, want [%s]", got, files[0].Name)
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
	var warnings []string
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	d.Warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	gateCleanReviewerRound(t, fx, d)
	recordDemoForTicket(t, d, fx.Ticket, []demoMediaSpec{{Name: "demo-1.png", Content: "bytes"}})
	_, addEnv := useGithubPRs(t, &d, "", "")
	addEnv("GH_STUB_BODY=## Demo\n\n- ./demo-1.png: still here, unrewritten\n")

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var warning string
	for _, w := range warnings {
		if strings.Contains(w, "unrewritten") {
			warning = w
		}
	}
	if warning == "" {
		t.Fatalf("warnings = %v, want one about an unrewritten media reference", warnings)
	}
	if !strings.Contains(warning, "demo-1.png") {
		t.Errorf("warning = %q, want it to name demo-1.png", warning)
	}
}

// TestPublishWarnsWhenGhLacksAttachSupport: an installed gh with no --attach
// on either subcommand (GH_STUB_NO_ATTACH) still opens the pull request, with
// no media and a warning naming why - never a silent pull request with
// broken ./<name> links and no explanation.
func TestPublishWarnsWhenGhLacksAttachSupport(t *testing.T) {
	t.Parallel()
	var warnings []string
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	d.Warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	gateCleanReviewerRound(t, fx, d)
	recordDemoForTicket(t, d, fx.Ticket, []demoMediaSpec{{Name: "demo-1.png", Content: "bytes"}})
	logFile, addEnv := useGithubPRs(t, &d, "", "")
	addEnv("GH_STUB_NO_ATTACH=1")

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGhCalls(t, logFile)
	create := findGhCall(calls, "pr", "create")
	if create == nil {
		t.Fatal("no logged pr create call")
	}
	if len(attachedFiles(create.Argv)) != 0 {
		t.Errorf("pr create argv %v carries --attach despite no support", create.Argv)
	}

	var warning string
	for _, w := range warnings {
		if strings.Contains(w, "--attach") {
			warning = w
		}
	}
	if warning == "" {
		t.Fatalf("warnings = %v, want one naming the installed gh's lack of --attach support", warnings)
	}
}
