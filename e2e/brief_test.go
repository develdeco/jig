package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// illustrationHTML is the fixture ticket's illustration in these tests.
const illustrationHTML = "<!doctype html><h1>The picture</h1><p>a mock of the clamp</p>"

// briefViewerEnv is env with the variables that would make the viewer print
// paths instead of serving: the host the suite runs on may be a remote
// session.
func briefViewerEnv(env jigEnv) jigEnv { return env.without("CLAUDE_CODE_REMOTE") }

// writeIllustration writes the fixture ticket's illustration.html.
func writeIllustration(t *testing.T, fx *fixture.Fixture, html string) string {
	t.Helper()
	path := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, store.IllustrationFile)
	writeFileOrFatal(t, path, []byte(html))
	return path
}

// viewerURL reads the viewer's output until it prints its url.
func viewerURL(t *testing.T, p *jigProc) string {
	t.Helper()
	for {
		line, ok := p.Line(30 * time.Second)
		if !ok {
			t.Fatalf("jig brief show printed no url:\n%s", p.Stop().Stdout)
		}
		if v, found := strings.CutPrefix(line, "  url: "); found {
			return strings.Trim(v, `"`)
		}
	}
}

// httpGet fetches url and returns the status and body.
func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

// TestBriefShowServesIllustration: `jig brief show` starts a viewer on
// 127.0.0.1, prints its url, serves the brief's sections as a page with a link
// to the illustration, serves the illustration, and where the platform lets a
// test interrupt a process, ends 0 when interrupted.
func TestBriefShowServesIllustration(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	writeIllustration(t, fx, illustrationHTML)

	p := startJig(t, briefViewerEnv(env), fx.StoreDir, "brief", "show", fx.Ticket, "--port", "0")
	url := viewerURL(t, p)
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("the viewer's url is %q, want one on 127.0.0.1", url)
	}

	code, page := httpGet(t, url)
	if code != 200 {
		t.Fatalf("GET %s: %d", url, code)
	}
	for _, want := range []string{"Slice A - failing clamp", "Slice D - rig smoke", "<h2>Goal</h2>", `href="/illustration.html"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q:\n%s", want, page)
		}
	}
	if code, ill := httpGet(t, url+"illustration.html"); code != 200 || ill != illustrationHTML {
		t.Errorf("GET illustration.html: %d %q, want the file as it is", code, ill)
	}

	if runtime.GOOS == "windows" {
		// A console interrupt cannot be sent to one process from here; the
		// run is ended by Stop, which kills it.
		p.Stop()
		return
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if res := p.Stop(); res.Code != 0 {
		t.Errorf("jig brief show exit %d after an interrupt, want 0\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
}

// TestBriefShowPrintsPathsWhenRemote: with CLAUDE_CODE_REMOTE set, where a
// browser cannot reach the session's 127.0.0.1, `jig brief show` prints the
// paths of the brief and the illustration and ends, serving nothing.
func TestBriefShowPrintsPathsWhenRemote(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	ill := writeIllustration(t, fx, illustrationHTML)

	p := startJig(t, env.with("CLAUDE_CODE_REMOTE=1"), fx.StoreDir, "brief", "show", fx.Ticket)
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		p.Stop()
		t.Fatal("jig brief show kept running with CLAUDE_CODE_REMOTE set: it serves where no browser can reach it")
	}
	res := p.Stop()
	brief := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "brief.md")
	for _, want := range []string{"paths:\n", "  brief: ", filepath.Base(brief), "  illustration: ", filepath.Base(ill), "CLAUDE_CODE_REMOTE is set"} {
		if res.Code != 0 || !strings.Contains(res.Stdout, want) {
			t.Fatalf("jig brief show when remote: exit %d, lacks %q:\n%s%s", res.Code, want, res.Stdout, res.Stderr)
		}
	}
	if strings.Contains(res.Stdout, "url:") {
		t.Errorf("the output names a viewer:\n%s", res.Stdout)
	}
}

// TestBriefShowText: `--text` prints the sections table and each section's
// body, for the TUI's detail screen to read.
func TestBriefShowText(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	r := runJig(t, env, fx.StoreDir, "brief", "show", fx.Ticket, "--text")
	if r.Code != 0 {
		t.Fatalf("jig brief show --text exit = %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{
		"brief:\n  ticket: JIG-1\n  title: Fixture ticket brief\n  illustration: no\n",
		"sections[5]{heading,summary}:\n  Goal,",
		"  Slice A - failing clamp,",
		"section: Goal\n  Give the fixture repo two small",
		"section: Slice A - failing clamp\n  `alpha.Clamp` is committed broken",
		"section: Slice D - rig smoke\n",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig brief show --text lacks %q:\n%s", want, r.Stdout)
		}
	}
}

// TestIllustrationEditWithdrawsApproval: an approval covers the illustration
// as it covers the brief, so editing, adding or removing illustration.html
// leaves the brief as changed since it was approved, and an unattended claim
// is refused until the brief is approved again. A brief with no illustration
// is hashed as it always was, so an approval recorded before there were
// illustrations is still current.
func TestIllustrationEditWithdrawsApproval(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "brief.md")
	illPath := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, store.IllustrationFile)
	plain := sha256.Sum256(readFileOrFatal(t, briefPath))
	plainHex := hex.EncodeToString(plain[:])

	approvalState := func() string {
		t.Helper()
		r := runJig(t, env, fx.StoreDir, "status", fx.Ticket)
		if r.Code != 0 {
			t.Fatalf("jig status exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		for _, state := range []string{"none", "current", "changed"} {
			if strings.Contains(r.Stdout, "  approval: "+state+"\n") {
				return state
			}
		}
		t.Fatalf("jig status names no approval state:\n%s", r.Stdout)
		return ""
	}
	validate := func() {
		t.Helper()
		if r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket); r.Code != 0 || !strings.Contains(r.Stdout, "  result: recorded\n") {
			t.Fatalf("validate: exit %d, want the approval recorded\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
	}
	claimRefused := func(label string) {
		t.Helper()
		r := runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--unattended", "--session", "s-1")
		if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_NOT_APPROVED") || !strings.Contains(r.Stdout, "changed after it was approved") {
			t.Fatalf("claim %s: exit %d, want 1 with CLAIM_NOT_APPROVED\n%s", label, r.Code, r.Stdout)
		}
	}

	// With no illustration the approval is of the brief's own sha256: the value
	// an older jig wrote, so the approvals already recorded stay valid.
	validate()
	if a, ok, err := st.ReadApproval(fx.Ticket); err != nil || !ok || a.BriefSHA256 != plainHex {
		t.Fatalf("approval = %+v, %v, %v, want the sha256 of brief.md, %s", a, ok, err, plainHex)
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("after validate the approval is %s, want current", got)
	}
	if err := st.WriteApproval(fx.Ticket, store.Approval{By: "Older Jig", At: "2026-10-01T00:00:00Z", BriefSHA256: plainHex, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("an approval recorded before illustrations is %s, want current", got)
	}

	// An illustration added after the approval withdraws it.
	writeFileOrFatal(t, illPath, []byte(illustrationHTML))
	if got := approvalState(); got != "changed" {
		t.Fatalf("after an illustration was added the approval is %s, want changed", got)
	}
	claimRefused("after the illustration was added")

	// Approving again covers the illustration; editing it withdraws that.
	validate()
	approved, _, _ := st.ReadApproval(fx.Ticket)
	if approved.BriefSHA256 == plainHex {
		t.Fatal("the approval of a brief with an illustration is the brief's own hash: it does not cover the illustration")
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("after validating with an illustration the approval is %s, want current", got)
	}
	writeFileOrFatal(t, illPath, []byte(illustrationHTML+"<p>redrawn</p>"))
	if got := approvalState(); got != "changed" {
		t.Fatalf("after the illustration was edited the approval is %s, want changed", got)
	}
	claimRefused("after the illustration was edited")

	// Removing it changes the brief again, and the next approval is the
	// brief's own hash once more.
	validate()
	if err := os.Remove(illPath); err != nil {
		t.Fatal(err)
	}
	if got := approvalState(); got != "changed" {
		t.Fatalf("after the illustration was removed the approval is %s, want changed", got)
	}
	validate()
	if a, _, _ := st.ReadApproval(fx.Ticket); a.BriefSHA256 != plainHex {
		t.Errorf("approval after the illustration was removed = %s, want the sha256 of brief.md, %s", a.BriefSHA256, plainHex)
	}
}
