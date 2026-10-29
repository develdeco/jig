package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

const (
	showTicket = "JIG-2"
	showBrief  = "# Show it\n\nIntro that belongs to no section.\n\n## Goal\n\nMake the thing visible.\nIt must work offline.\nThird line, not in the summary.\n\n## Steps\n\n- build it\n- show it\n\n  indented after a blank\n\n## Empty\n"
	showHTML   = "<h1>The picture</h1><script>1</script>"
)

// briefFx is where a briefRig keeps its jig home and its store, and the repo of
// the one rig that has a clone.
type briefFx struct {
	Home, StoreDir, RepoDir string
}

// briefRig is a store with a ticket, JIG-2, whose brief is showBrief and, when
// illustration is true, whose illustration is showHTML. The store is made of
// files alone, so no test starts git but the one that needs the repo of a
// branch (newBriefRigInRepo).
type briefRig struct {
	t  *testing.T
	fx briefFx
	st *store.Store
}

func newBriefRig(t *testing.T, illustration bool) briefRig {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 2\nname: demo\nkeys:\n  JIG: everything in demo\ntrackers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return briefRigOf(t, briefFx{Home: t.TempDir(), StoreDir: root}, st, illustration)
}

// newBriefRigInRepo is newBriefRig on the fixture: the store has a clone, for
// the verb that names the ticket after the working branch.
func newBriefRigInRepo(t *testing.T, illustration bool) briefRig {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return briefRigOf(t, briefFx{Home: fx.Home, StoreDir: fx.StoreDir, RepoDir: fx.RepoDir}, st, illustration)
}

func briefRigOf(t *testing.T, fx briefFx, st *store.Store, illustration bool) briefRig {
	t.Helper()
	if err := st.CreateTicketRecord(showTicket, store.Ticket{Title: "Show the thing", Slug: "show-the-thing"}); err != nil {
		t.Fatal(err)
	}
	r := briefRig{t: t, fx: fx, st: st}
	r.write("brief.md", showBrief)
	if illustration {
		r.write(store.IllustrationFile, showHTML)
	}
	return r
}

func (r briefRig) write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.st.TicketDir(showTicket), name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r briefRig) path(name string) string { return filepath.Join(r.st.TicketDir(showTicket), name) }

// show runs `jig brief show` with args against the rig's store, in e.
func (r briefRig) show(t *testing.T, e env, args ...string) (string, int) {
	t.Helper()
	return runMain(t, e, "", append([]string{"brief", "show"}, append(args, "--store", r.fx.StoreDir)...)...)
}

// remoteTestEnv is the env of a session the harness says is remote.
func remoteTestEnv(home string) env {
	userHome := filepath.Join(home, "operator-home")
	return envFrom(map[string]string{"JIG_HOME": home, "HOME": userHome, "USERPROFILE": userHome, remoteEnv: "true"})
}

// TestBriefShowText: --text prints the brief block, a table of each section
// with the first line or two of its body, then each section's body indented
// two spaces, a blank line staying empty and an empty section a header alone.
func TestBriefShowText(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	e := testEnv(r.fx.Home)

	want := "brief:\n" +
		"  ticket: JIG-2\n" +
		"  title: Show it\n" +
		"  illustration: yes\n" +
		"sections[3]{heading,summary}:\n" +
		"  Goal,Make the thing visible. It must work offline.\n" +
		"  Steps,- build it - show it\n" +
		"  Empty,\n" +
		"section: Goal\n" +
		"  Make the thing visible.\n" +
		"  It must work offline.\n" +
		"  Third line, not in the summary.\n" +
		"section: Steps\n" +
		"  - build it\n" +
		"  - show it\n" +
		"\n" +
		"    indented after a blank\n" +
		"section: Empty\n" +
		"help[1]:\n" +
		"  `jig brief show JIG-2` serves the brief and its illustration on 127.0.0.1 for a browser\n"
	out, code := r.show(t, e, showTicket, "--text")
	if code != 0 || out != want {
		t.Fatalf("jig brief show --text: exit %d, want 0 and\n%s\ngot\n%s", code, want, out)
	}

	// With no illustration the block says so, and the rest is the same.
	if err := os.Remove(r.path(store.IllustrationFile)); err != nil {
		t.Fatal(err)
	}
	out, code = r.show(t, e, showTicket, "--text")
	if wantNo := strings.Replace(want, "illustration: yes", "illustration: no", 1); code != 0 || out != wantNo {
		t.Fatalf("jig brief show --text with no illustration: exit %d, got\n%s", code, out)
	}
}

// TestBriefShowTextIsTheSameWhereverTheHarnessRuns: --text prints text even
// when the session is remote, since it needs no browser.
func TestBriefShowTextIsTheSameWhereverTheHarnessRuns(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, false)
	local, _ := r.show(t, testEnv(r.fx.Home), showTicket, "--text")
	remote, code := r.show(t, remoteTestEnv(r.fx.Home), showTicket, "--text")
	if code != 0 || remote != local || !strings.HasPrefix(remote, "brief:\n") {
		t.Fatalf("jig brief show --text when remote: exit %d, got\n%s\nwant what it prints elsewhere:\n%s", code, remote, local)
	}
}

// TestBriefShowPath: --path prints the absolute paths of the brief and, when
// there is one, the illustration, and starts no viewer.
func TestBriefShowPath(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	e := testEnv(r.fx.Home)

	out, code := r.show(t, e, showTicket, "--path")
	for _, want := range []string{"paths:\n", r.pathLine("brief", "brief.md"), r.pathLine("illustration", "illustration.html")} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("jig brief show --path: exit %d, lacks %q:\n%s", code, want, out)
		}
	}
	if !filepath.IsAbs(r.path("brief.md")) || strings.Contains(out, "url:") {
		t.Errorf("the paths are not absolute or the output names a viewer:\n%s", out)
	}

	if err := os.Remove(r.path(store.IllustrationFile)); err != nil {
		t.Fatal(err)
	}
	out, code = r.show(t, e, showTicket, "--path")
	if code != 0 || !strings.Contains(out, "  brief: ") || strings.Contains(out, "illustration") {
		t.Errorf("jig brief show --path with no illustration: exit %d, want the brief's path alone:\n%s", code, out)
	}
}

// TestBriefShowPrintsPathsWhenRemote: when the harness says the session is
// remote (CLAUDE_CODE_REMOTE is set) the paths are printed, with the reason,
// and nothing is served: the run ends by itself instead of waiting to be
// interrupted.
func TestBriefShowPrintsPathsWhenRemote(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	// A viewer that started would run until this context ended.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e := remoteTestEnv(r.fx.Home).withContext(ctx)

	out, code := r.show(t, e, showTicket)
	if ctx.Err() != nil {
		t.Fatalf("jig brief show kept running with CLAUDE_CODE_REMOTE set:\n%s", out)
	}
	for _, want := range []string{r.pathLine("brief", "brief.md"), r.pathLine("illustration", "illustration.html"), "CLAUDE_CODE_REMOTE is set"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("jig brief show when remote: exit %d, lacks %q:\n%s", code, want, out)
		}
	}
	if strings.Contains(out, "url:") || strings.Contains(out, "127.0.0.1:") {
		t.Errorf("the output names a viewer:\n%s", out)
	}
}

// startViewer runs `jig brief show ... --port port` in the background and
// returns the url it printed, the result channel and the function that stops
// it.
func startViewer(t *testing.T, r briefRig, port string, args ...string) (url string, done <-chan int, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := testEnv(r.fx.Home).withContext(ctx)
	var out syncBuffer
	result := make(chan int, 1)
	go func() {
		result <- run(e, append([]string{"brief", "show", showTicket, "--store", r.fx.StoreDir, "--port", port}, args...), &out, strings.NewReader(""))
	}()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(out.String(), "\n") {
			if v, ok := strings.CutPrefix(line, "  url: "); ok {
				return strings.Trim(v, `"`), result, cancel
			}
		}
		select {
		case code := <-result:
			t.Fatalf("jig brief show ended with %d before it printed its url:\n%s", code, out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("jig brief show printed no url in 30s:\n%s", out.String())
	return "", nil, nil
}

// fetch gets url with a client that gives up, and returns the status and body.
func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestBriefShowServesIllustration: the viewer prints its address, serves a
// page with the brief's sections and a link to the illustration, serves the
// illustration as it is, shows an edit on reload, and stops with the run's
// context, after which the address answers no more.
func TestBriefShowServesIllustration(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	url, done, stop := startViewer(t, r, "0")
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("the viewer's url is %q, want one on 127.0.0.1", url)
	}

	code, page := fetch(t, url)
	if code != 200 {
		t.Fatalf("GET %s: %d", url, code)
	}
	for _, want := range []string{"<h2>Goal</h2>", "<h2>Steps</h2>", "<h2>Empty</h2>", "Make the thing visible.", `href="/illustration.html"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q:\n%s", want, page)
		}
	}
	code, ill := fetch(t, url+"illustration.html")
	if code != 200 || ill != showHTML {
		t.Fatalf("GET illustration.html: %d %q, want the file as it is", code, ill)
	}

	r.write("brief.md", showBrief+"\n## Late\n\nAdded while the viewer ran.\n")
	if _, page := fetch(t, url); !strings.Contains(page, "Added while the viewer ran.") {
		t.Errorf("a reload does not show the edit:\n%s", page)
	}

	stop()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("jig brief show exit %d when its context ended, want 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("jig brief show did not stop when its context ended")
	}
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(url); err == nil {
		resp.Body.Close()
		t.Errorf("GET %s still answers after the viewer stopped", url)
	}
}

// TestBriefShowWithNoIllustrationLinksNone: a brief with no illustration is
// served without a link, and the illustration's address is not found.
func TestBriefShowWithNoIllustrationLinksNone(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, false)
	url, _, stop := startViewer(t, r, "0")
	defer stop()
	if code, page := fetch(t, url); code != 200 || strings.Contains(page, "illustration.html") {
		t.Errorf("GET /: %d, want a page with no link\n%s", code, page)
	}
	if code, _ := fetch(t, url+"illustration.html"); code != 404 {
		t.Errorf("GET /illustration.html: %d, want 404", code)
	}
}

// TestBriefShowTakesPortFromFlag: --port names the port the viewer listens
// on; one that is taken is refused with the way out.
func TestBriefShowTakesPortFromFlag(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, false)
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	taken := strconv.Itoa(hold.Addr().(*net.TCPAddr).Port)
	out, code := r.show(t, testEnv(r.fx.Home), showTicket, "--port", taken)
	for _, want := range []string{"cannot serve the brief on 127.0.0.1:" + taken, "code: VIEWER_UNAVAILABLE", "--port 0"} {
		if code != 1 || !strings.Contains(out, want) {
			t.Errorf("jig brief show on a taken port: exit %d, lacks %q:\n%s", code, want, out)
		}
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	url, _, stop := startViewer(t, r, strconv.Itoa(port))
	defer stop()
	if want := "http://127.0.0.1:" + strconv.Itoa(port) + "/"; url != want {
		t.Errorf("the viewer's url is %q, want %q", url, want)
	}
}

// TestBriefShowRefusals: what it cannot show it says why, and exits 2 or 1
// as the other verbs do.
func TestBriefShowRefusals(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, false)
	e := testEnv(r.fx.Home)
	if err := r.st.CreateTicketRecord("JIG-3", store.Ticket{Title: "No brief yet"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		code int
		want []string
	}{
		{"no brief", []string{"JIG-3", "--path"}, 2, []string{"ticket JIG-3 has no brief.md", "jig validate JIG-3 --check"}},
		{"unknown ticket", []string{"NOPE-1", "--path"}, 2, []string{"ticket NOPE-1 not found"}},
		{"both alternatives", []string{showTicket, "--path", "--text"}, 2, []string{"--path and --text are alternatives"}},
		{"a port out of range", []string{showTicket, "--port", "70000"}, 2, []string{"--port 70000 is not a port"}},
		{"two tickets", []string{showTicket, "JIG-1", "--path"}, 2, []string{"takes at most one ticket"}},
	} {
		out, code := r.show(t, e, c.args...)
		for _, want := range c.want {
			if code != c.code || !strings.Contains(out, want) {
				t.Errorf("%s: exit %d, want %d with %q:\n%s", c.name, code, c.code, want, out)
			}
		}
	}
}

// TestBriefShowTakesTheTicketTheBranchNames: with no ticket argument the
// ticket is the one the working branch is for; on a branch that names none it
// asks for one.
func TestBriefShowTakesTheTicketTheBranchNames(t *testing.T) {
	t.Parallel()
	r := newBriefRigInRepo(t, false)
	e := testEnv(r.fx.Home).inDir(r.fx.RepoDir)

	out, code := r.show(t, e, "--path")
	if code != 2 || !strings.Contains(out, "cannot tell which ticket this is") || !strings.Contains(out, "jig brief show T-1") {
		t.Fatalf("on a branch that names no ticket: exit %d, want a refusal that asks for one:\n%s", code, out)
	}

	if _, err := gitx.Run(r.fx.RepoDir, "switch", "-c", showTicket+"-show-the-thing"); err != nil {
		t.Fatal(err)
	}
	out, code = r.show(t, e, "--path")
	if want := r.pathLine("brief", "brief.md"); code != 0 || !strings.Contains(out, want) {
		t.Fatalf("on the branch of %s: exit %d, lacks %q:\n%s", showTicket, code, want, out)
	}
}

// pathLine is the line of a paths block that names the file name under key.
func (r briefRig) pathLine(key, name string) string {
	return "  " + key + ": " + axi.Quote(r.path(name)) + "\n"
}
