package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// commentingClient is a stubGitHub that keeps the comments it is asked to post.
type commentingClient struct {
	stubGitHub
	comments []string
}

func (c *commentingClient) AddComment(_ context.Context, _, body string) error {
	c.comments = append(c.comments, body)
	return nil
}

// TestTrackersSyncPlanComment drives `jig trackers sync` over a model chart:
// the dry run names the comment it would post and posts none; the sync posts
// it and prints it; a second sync is silent; a changed title posts again.
func TestTrackersSyncPlanComment(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)

	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	for id, tk := range map[string]store.Ticket{
		"DEMO-1": {Title: "The need", Body: "Customers want a thing.", Kind: store.KindIssue},
		"DEMO-2": {Title: "First task", Body: "Build the first part.\nWith detail."},
	} {
		if err := st.CreateTicketRecord(id, tk); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(clone, "charts", "plan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "map.md"), []byte("# Chart: plan\n\nthe plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte("issue: DEMO-1\ntickets:\n  - id: DEMO-2\n    title: First task\n  - title: Third task\n    body: Not graduated yet.\n    blocked_by:\n      - ref: DEMO-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "model chart"); err != nil {
		t.Fatal(err)
	}

	client := &commentingClient{}
	e.mirrorClient = client
	sync := func(args ...string) string {
		t.Helper()
		var buf bytes.Buffer
		a := append([]string{"trackers", "sync", "--store", clone}, args...)
		if code := run(e, a, &buf, strings.NewReader("")); code != 0 {
			t.Fatalf("jig %v: exit %d\n%s", a, code, buf.String())
		}
		return buf.String()
	}

	// Nothing has an issue yet, so the dry run has no comment to preview.
	if out := sync("--dry-run"); strings.Contains(out, "plan_comment") {
		t.Errorf("dry run before any issue = %s, want no plan comment", out)
	}

	out := sync()
	if !strings.Contains(out, "plan_commented") || !strings.Contains(out, "DEMO-1") {
		t.Fatalf("sync output = %s, want a plan_commented row for DEMO-1", out)
	}
	if len(client.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(client.comments))
	}
	c := client.comments[0]
	for _, want := range []string{"- DEMO-2 First task: Build the first part.", "- not graduated Third task: Not graduated yet. (waits for DEMO-2)", "charts/plan/"} {
		if !strings.Contains(c, want) {
			t.Errorf("comment = %q, want it to contain %q", c, want)
		}
	}

	if out := sync(); strings.Contains(out, "plan_comment") || len(client.comments) != 1 {
		t.Errorf("second sync = %s with %d comments, want silence", out, len(client.comments))
	}

	path := st.TicketFilePath("DEMO-2")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "First task", "Renamed task", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := sync("--dry-run"); !strings.Contains(out, "would_plan_comment") || len(client.comments) != 1 {
		t.Errorf("dry run after a rename = %s with %d comments, want a preview and no post", out, len(client.comments))
	}
	sync()
	if len(client.comments) != 2 || !strings.Contains(client.comments[1], "Renamed task") {
		t.Errorf("comments = %q, want a second naming the renamed task", client.comments)
	}
}
