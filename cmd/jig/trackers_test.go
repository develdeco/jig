package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/mirror/github"
)

// publishSafetyTestEmail is a sample publish-safety email hit: a reserved
// .test domain (RFC 2606) names no real company, but the mirror's scanner
// exempts only example.com/.org/.net/.invalid, so this still trips the
// built-in email pattern. Built from parts so this line itself does not
// read as a contiguous email address.
const publishSafetyTestEmail = "someone" + "@" + "example.test"

// TestTrackersSyncNoGitHubEntryIsANoOp checks that `jig trackers sync`
// against a store with no trackers: github: entry (the default
// newTestOriginClone's project.yaml) says so and exits 0, reaching no
// network.
func TestTrackersSyncNoGitHubEntryIsANoOp(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)

	var buf bytes.Buffer
	code := run(e, []string{"trackers", "sync", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig trackers sync: exit %d\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "no github entry configured") {
		t.Fatalf("output = %s, want it to say no github entry is configured", buf.String())
	}
}

// newTestOriginCloneWithGitHubTracker is newTestOriginClone, but its
// project.yaml declares a trackers: github: entry, so Sync has work to
// report (brief.md#The trackers entry).
func newTestOriginCloneWithGitHubTracker(t *testing.T, dir string) (remote string) {
	t.Helper()
	parent := filepath.Dir(dir)
	remote = filepath.Join(parent, filepath.Base(dir)+"-remote.git")

	if _, err := gitx.Run("", "init", "--bare", "-b", "main", remote); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(parent, filepath.Base(dir)+"-seed")
	if _, err := gitx.Run("", "clone", remote, seed); err != nil {
		t.Fatal(err)
	}
	projectYAML := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
`
	if err := os.WriteFile(filepath.Join(seed, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "jig: init store"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run("", "clone", remote, dir); err != nil {
		t.Fatal(err)
	}
	return remote
}

// stubFailingMirrorClient returns a client for env.mirrorClient pointed at a
// fake GraphQL server (httptest) that answers every request with a server
// error, so a checkpoint's own sync - run by `jig ticket new` once its claim
// lands, among others - fails fast and hermetically (never reaching gh or
// github.com, per brief.md's test constraints) and leaves no record behind,
// reported as a warning rather than failing the command.
func stubFailingMirrorClient(t *testing.T) github.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := github.New(server.URL, "test-token")
	client.BackoffBase = time.Millisecond
	client.MutationInterval = time.Millisecond
	return client
}

// TestTrackersSyncDryRunReportsAWouldCreateTicket checks `jig trackers sync
// --dry-run` end to end through the CLI: a ticket with no record reports as
// a would-create row, reaching no network (mirror.Sync builds no client in
// a dry run) and writing nothing to the store.
func TestTrackersSyncDryRunReportsAWouldCreateTicket(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)

	// jig ticket new's own checkpoint sync must not reach a real GitHub
	// (brief.md's test constraints); pointed at a server that always fails,
	// it leaves the ticket's record unwritten, which is the state this test
	// wants `jig trackers sync --dry-run` to see.
	e.mirrorClient = stubFailingMirrorClient(t)

	var buf bytes.Buffer
	code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}

	buf.Reset()
	code = run(e, []string{"trackers", "sync", "--dry-run", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig trackers sync --dry-run: exit %d\n%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "would_create") || !strings.Contains(out, "DEMO-1") {
		t.Fatalf("output = %s, want a would_create row naming DEMO-1", out)
	}
	if _, err := os.Stat(filepath.Join(clone, "tickets", "DEMO-1", "tracker", "github.yaml")); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote a record (stat err %v), want nothing written", err)
	}
}

// TestTrackersSyncDryRunExitsNonZeroOnAPublishSafetyHit checks `jig trackers
// sync --dry-run`'s side of the "a publish-safety hit skips that one ticket
// or chart" critical path (brief.md#Publish safety, #jig trackers sync):
// a ticket title that trips the built-in email pattern is reported skipped,
// and the command exits non-zero.
func TestTrackersSyncDryRunExitsNonZeroOnAPublishSafetyHit(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = stubFailingMirrorClient(t)

	var buf bytes.Buffer
	code := run(e, []string{"ticket", "new", "--title", "Contact " + publishSafetyTestEmail + " for this", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}

	buf.Reset()
	code = run(e, []string{"trackers", "sync", "--dry-run", "--store", clone}, &buf, strings.NewReader(""))
	if code == 0 {
		t.Fatalf("jig trackers sync --dry-run: exit 0, want non-zero when a ticket was skipped\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "DEMO-1") {
		t.Fatalf("output = %s, want a skipped row naming DEMO-1", out)
	}
}

// TestCheckpointSyncFailureReportsWarningAndStorePushSucceeds is the "a
// store checkpoint inside a command: it syncs, and a GitHub failure leaves
// the command successful with a warning" seam (brief.md#Seams), exercised
// at Store.Push's own checkpoint hook, which resolveStore wires.
// env.mirrorClient points the hook's sync at a fake GraphQL server
// (httptest) that answers every request with a server error - never gh or
// github.com, per brief.md's test constraints - so the failure is the
// code's own doing, not a fact about the machine running the test. Push
// must still succeed, and the warning resolveStore's Store.Warn wiring
// reports must land in the command's own structured output, not bare
// stderr. TestTicketNewRunsItsOwnCheckpointSync covers the same seam's
// other caller: `jig ticket new`'s explicit call once its Claim lands.
func TestCheckpointSyncFailureReportsWarningAndStorePushSucceeds(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := github.New(server.URL, "test-token")
	// A 500 is retryable, so the client spends its 4 attempts here
	// (brief.md#The GitHub client); shrink its backoff so this test does
	// not wait the production 1s, 2s, 4s out.
	client.BackoffBase = time.Millisecond
	client.MutationInterval = time.Millisecond
	e.mirrorClient = client

	var buf bytes.Buffer
	st, _, _, _, err := resolveStore(e, clone, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Push("test: checkpoint"); err != nil {
		t.Fatalf("Push: %v, want nil even though its checkpoint's tracker sync failed", err)
	}
	if hits.Load() == 0 {
		t.Fatal("fake GraphQL server saw no request; the checkpoint hook never ran its sync")
	}
	if !strings.Contains(buf.String(), "warning") {
		t.Fatalf("output missing the checkpoint sync's warning:\n%s", buf.String())
	}
}

// stubGitHub is a github.Client that answers every read with what this
// package's own checkpoint test needs and records nothing: the mirror
// package's tests own what a sync writes, and these cmd tests own only what
// reaches the command's output. Its issue repo is public (so a title is
// scanned, brief.md#Publish safety), every issue it is asked about carries
// issueTitle (a hand edit, when that differs from what the store renders),
// and its project resolves with every field in place.
type stubGitHub struct {
	issueTitle string
}

func (s *stubGitHub) CreateIssue(context.Context, string, string, string, string) (github.Issue, error) {
	return github.Issue{Number: 1, NodeID: "NODE_1"}, nil
}
func (s *stubGitHub) CloseIssueNotPlanned(context.Context, string) error { return nil }
func (s *stubGitHub) AddComment(context.Context, string, string) error   { return nil }
func (s *stubGitHub) PullRequestsByHead(context.Context, string, string, string) ([]github.PullRequest, error) {
	return nil, nil
}
func (s *stubGitHub) PullRequestsByIDs(context.Context, []string) ([]github.PullRequest, error) {
	return nil, nil
}
func (s *stubGitHub) AddSubIssue(context.Context, string, string) error     { return nil }
func (s *stubGitHub) AddBlockedBy(context.Context, string, string) error    { return nil }
func (s *stubGitHub) RemoveBlockedBy(context.Context, string, string) error { return nil }
func (s *stubGitHub) RemoveSubIssue(context.Context, string, string) error  { return nil }
func (s *stubGitHub) EnsureProject(context.Context, string, bool, int, string, string) (github.ProjectV2, error) {
	return github.ProjectV2{
		ID:            "PROJECT_1",
		StatusFieldID: "FIELD_STATUS",
		StatusOptions: map[string]string{
			"Backlog": "OPT_Backlog", "Briefed": "OPT_Briefed", "In progress": "OPT_In_progress",
			"In review": "OPT_In_review", "Done": "OPT_Done",
		},
		StoreIDFieldID: "FIELD_STOREID",
	}, nil
}
func (s *stubGitHub) LookupProject(context.Context, string, bool, int) (bool, error) {
	return true, nil
}
func (s *stubGitHub) PlaceItem(context.Context, github.ProjectV2, string, string, string) (string, error) {
	return "ITEM_1", nil
}
func (s *stubGitHub) RepositoryIsPublic(context.Context, string, string) (bool, error) {
	return true, nil
}
func (s *stubGitHub) FetchIssue(context.Context, string) (github.IssueState, error) {
	return github.IssueState{Title: s.issueTitle, Open: true}, nil
}
func (s *stubGitHub) UpdateIssue(context.Context, string, string, string) error { return nil }
func (s *stubGitHub) CloseIssueCompleted(context.Context, string) error         { return nil }
func (s *stubGitHub) ReopenIssue(context.Context, string) error                 { return nil }
func (s *stubGitHub) ItemFieldValues(context.Context, string) (string, string, error) {
	return "", "", nil
}

// TestTicketNewRunsItsOwnCheckpointSync is the gate finding's own fix
// (r2-f8): `jig ticket new` reaches the store through Store.Claim alone,
// which stays hook-free (the mirror's own claims, through claimIssue,
// commit new records through Claim too, and hooking Claim would re-enter
// the mirror), so the command runs the checkpoint sync itself, explicitly,
// once its claim has landed - driven here through the real command, not
// resolveStore and Store.Push directly. A newly minted ticket must have a
// GitHub issue recorded before the command returns, rather than waiting on
// some later, unrelated command's Push.
func TestTicketNewRunsItsOwnCheckpointSync(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)

	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}

	data, err := os.ReadFile(filepath.Join(clone, "tickets", "DEMO-1", "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read DEMO-1's github.yaml: %v, want jig ticket new's own checkpoint sync to have created it", err)
	}
	if !strings.Contains(string(data), "issue: 1") {
		t.Fatalf("github.yaml = %s, want issue: 1 from the checkpoint sync's CreateIssue", data)
	}
}

// TestCheckpointSyncReportsDriftAndSkippedThroughTheCommandsOutput is
// brief.md#Ownership and drift's own reporting rule - "drift lines print in
// the output of the command whose checkpoint found them, and in jig trackers
// sync's" - plus brief.md#Publish safety's "the sync names the ticket or
// chart, the line number and the line", at the checkpoint side of those two
// places. The store has one ticket whose issue was edited on GitHub (drift
// against what the last sync recorded) and one whose title trips the
// built-in email pattern (skipped, since the issue repo is public), and the
// one sync a checkpoint runs must report both through the writer the
// resolving command handed resolveStore - not swallow the report, as the
// hook did before this slice.
func TestCheckpointSyncReportsDriftAndSkippedThroughTheCommandsOutput(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)

	// jig ticket new's own checkpoint sync must not reach a real GitHub
	// either (brief.md's test constraints); pointed at a server that always
	// fails, it leaves both tickets' records unwritten, which is the state
	// this test's own manual record (for DEMO-1) and the stubGitHub sync below
	// (for both DEMO-1 and DEMO-2) need to start from.
	e.mirrorClient = stubFailingMirrorClient(t)

	var setup bytes.Buffer
	for _, title := range []string{"First ticket", "Contact " + publishSafetyTestEmail + " for this"} {
		if code := run(e, []string{"ticket", "new", "--title", title, "--store", clone}, &setup, strings.NewReader("")); code != 0 {
			t.Fatalf("jig ticket new %q: exit %d\n%s", title, code, setup.String())
		}
	}

	// DEMO-1 already has an issue, and the last sync recorded the title it
	// wrote; the stub reports GitHub carrying another one, so this sync
	// finds drift and overwrites it. DEMO-2 has no record, and never gets one.
	record := "# written by jig's GitHub mirror\n" +
		"repo: example/tracking\nissue: 7\nnode_id: NODE_1\nsynced:\n  title: First ticket\n  state: OPEN\n"
	recordPath := filepath.Join(clone, "tickets", "DEMO-1", "tracker", "github.yaml")
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	e.mirrorClient = &stubGitHub{issueTitle: "Renamed by hand on GitHub"}

	var buf bytes.Buffer
	st, _, _, _, err := resolveStore(e, clone, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Push("test: checkpoint"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "drift[") || !strings.Contains(out, "DEMO-1,7,title") {
		t.Fatalf("output missing the checkpoint's drift row for DEMO-1's title on issue 7:\n%s", out)
	}
	if !strings.Contains(out, "skipped[") || !strings.Contains(out, "DEMO-2,1,Contact "+publishSafetyTestEmail+" for this") {
		t.Fatalf("output missing the checkpoint's skipped row naming DEMO-2, its line number and the line:\n%s", out)
	}
}

// TestTrackersSyncUnknownSubcommand checks that a subcommand besides "sync"
// is refused as a VALIDATION_ERROR.
func TestTrackersSyncUnknownSubcommand(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	var buf bytes.Buffer
	code := run(e, []string{"trackers", "bogus"}, &buf, strings.NewReader(""))
	if code == 0 {
		t.Fatalf("jig trackers bogus: exit 0, want a refusal\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "VALIDATION_ERROR") {
		t.Fatalf("output = %s, want VALIDATION_ERROR", buf.String())
	}
}
