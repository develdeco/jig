package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newPlainRepo is an empty repository with one commit.
func newPlainRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"user.name", "tester"}, {"user.email", "tester@example.invalid"}} {
		if _, err := Run(dir, "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(dir, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunInputFeedsGitItsStandardInput: an object goes in with
// `hash-object -w --stdin`, a tree is built from it with `mktree`, and a
// commit with `commit-tree` - the plumbing that builds a commit on a branch
// nobody checks out, with no temporary file - and what was stored reads back
// byte for byte, empty content and content with no final newline included.
func TestRunInputFeedsGitItsStandardInput(t *testing.T) {
	dir := newPlainRepo(t)
	for _, content := range []string{"line one\nline two\n", "", "no final newline"} {
		blob, err := RunInput(dir, []byte(content), "hash-object", "-w", "--stdin")
		if err != nil || len(blob) < 40 {
			t.Fatalf("hash-object of %q = %q, %v", content, blob, err)
		}
		tree, err := RunInput(dir, []byte("100644 blob "+blob+"\tclaims.ndjson\n"), "mktree")
		if err != nil {
			t.Fatalf("mktree: %v", err)
		}
		commit, err := Run(dir, "-c", "user.name=who", "-c", "user.email=who@example.invalid", "commit-tree", tree, "-m", "plumbing")
		if err != nil {
			t.Fatalf("commit-tree: %v", err)
		}
		if _, err := Run(dir, "update-ref", "refs/heads/plumbing", commit); err != nil {
			t.Fatal(err)
		}
		got, err := RunRaw(dir, "show", "refs/heads/plumbing:claims.ndjson")
		if err != nil || got != content {
			t.Fatalf("stored %q, read back %q, %v", content, got, err)
		}
	}
	if out, err := Run(dir, "log", "-1", "--pretty=%an <%ae>", "refs/heads/plumbing"); err != nil || out != "who <who@example.invalid>" {
		t.Errorf("the commit-tree commit is by %q, %v", out, err)
	}
}

// TestRunInputReportsGitsStderr: a failure carries what git said, as Run's
// does, and the stdin did not turn into a hang.
func TestRunInputReportsGitsStderr(t *testing.T) {
	dir := newPlainRepo(t)
	_, err := RunInput(dir, []byte("not a tree line\n"), "mktree")
	if err == nil || !strings.Contains(err.Error(), "mktree") {
		t.Fatalf("mktree of garbage = %v, want an error naming the call", err)
	}
}

// TestRunInputEnvAppendsEnvironment: a pinned date reaches the commit.
func TestRunInputEnvAppendsEnvironment(t *testing.T) {
	dir := newPlainRepo(t)
	out, err := RunInputEnv(dir, []string{"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z"}, []byte(""), "hash-object", "--stdin")
	if err != nil || out == "" {
		t.Fatalf("RunInputEnv = %q, %v", out, err)
	}
	tree, err := Run(dir, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := RunInputEnv(dir, []string{"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z"}, []byte("message from stdin\n"), "commit-tree", tree, "-F", "-")
	if err != nil {
		t.Fatalf("commit-tree -F -: %v", err)
	}
	if got, _ := Run(dir, "log", "-1", "--pretty=%aI|%s", commit); got != "2026-01-02T03:04:05Z|message from stdin" {
		t.Errorf("commit = %q, want the pinned date and the message from stdin", got)
	}
}

// TestRefExists: a ref is found whether it is a loose file or packed, a
// missing one and a non-repository are false, and a ref that is a directory of
// refs is not a ref.
func TestRefExists(t *testing.T) {
	dir := newPlainRepo(t)
	if _, err := Run(dir, "branch", "writers/alice"); err != nil {
		t.Fatal(err)
	}
	check := func(when string, want map[string]bool) {
		t.Helper()
		for ref, w := range want {
			got, err := RefExists(dir, ref)
			if err != nil || got != w {
				t.Errorf("%s: RefExists(%s) = %v, %v; want %v", when, ref, got, err, w)
			}
		}
	}
	want := map[string]bool{
		"refs/heads/main":           true,
		"refs/heads/writers/alice":  true,
		"refs/heads/writers":        false, // a directory of refs
		"refs/heads/claims":         false,
		"refs/remotes/origin/claim": false,
	}
	check("loose", want)
	if _, err := Run(dir, "pack-refs", "--all", "--prune"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "main")); err == nil {
		t.Fatal("pack-refs --prune left the loose ref; the test needs it packed")
	}
	check("packed", want)

	if got, err := RefExists(t.TempDir(), "refs/heads/main"); err != nil || got {
		t.Errorf("RefExists in a directory with no repository = %v, %v; want false", got, err)
	}
}

// TestRefExistsInALinkedWorktree: a worktree has a .git file, so git is asked.
func TestRefExistsInALinkedWorktree(t *testing.T) {
	dir := newPlainRepo(t)
	if _, err := Run(dir, "branch", "claims"); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if _, err := Run(dir, "worktree", "add", "--detach", linked); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]bool{"refs/heads/claims": true, "refs/heads/nope": false} {
		if got, err := RefExists(linked, ref); err != nil || got != want {
			t.Errorf("RefExists(linked, %s) = %v, %v; want %v", ref, got, err, want)
		}
	}
}

// TestOpenRepoServesAStoreWithTheWritersAttributes: the union merge driver
// converts no content, so a repository whose .gitattributes is the writers
// attributes is still one gitx works on in process; any other attributes are
// not.
func TestOpenRepoServesAStoreWithTheWritersAttributes(t *testing.T) {
	for _, c := range []struct {
		name, attrs string
		want        bool
	}{
		{"store attributes", StoreAttributes, true},
		{"writers attributes", WritersAttributes, true},
		{"writers attributes with CRLF", strings.ReplaceAll(WritersAttributes, "\n", "\r\n"), true},
		{"a text conversion", "* text=auto\n", false},
		{"the writers line alone", "*.ndjson merge=union\n", false},
		{"writers attributes and another line", WritersAttributes + "*.md eol=crlf\n", false},
	} {
		dir := newPlainRepo(t)
		if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(c.attrs), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := OpenRepo(dir)
		if got := err == nil; got != c.want {
			t.Errorf("%s: OpenRepo served in process = %v, want %v (%v)", c.name, got, c.want, err)
		}
	}
}

// TestWritersAttributesAreTheStoreAttributesPlusUnion pins the one thing the
// two strings mean to differ by.
func TestWritersAttributesAreTheStoreAttributesPlusUnion(t *testing.T) {
	if WritersAttributes != StoreAttributes+"*.ndjson merge=union\n" {
		t.Errorf("WritersAttributes = %q", WritersAttributes)
	}
}
