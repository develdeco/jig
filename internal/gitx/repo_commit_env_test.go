package gitx

import (
	"errors"
	"testing"
)

// pinnedEnv is an identity and dates a test hands to CommitAllEnv in place of
// editing the process environment.
func pinnedEnv() []string {
	return []string{
		"GIT_AUTHOR_NAME=pinned author",
		"GIT_AUTHOR_EMAIL=author@example.invalid",
		"GIT_AUTHOR_DATE=1767225600 +0000",
		"GIT_COMMITTER_NAME=pinned committer",
		"GIT_COMMITTER_EMAIL=committer@example.invalid",
		"GIT_COMMITTER_DATE=1767225600 +0100",
	}
}

// TestCommitAllEnvMatchesTheGitProgram: the commit carries the tree, author,
// committer and dates the git program writes under the same environment, so
// its hash is the same: the commit is undone and made again by the git
// program, and nothing differs.
func TestCommitAllEnvMatchesTheGitProgram(t *testing.T) {
	dir := newStoreRepo(t)
	writeFileT(t, dir, "a.txt", "a\n")
	writeFileT(t, dir, "sub/b.txt", "b\n")
	committed, err := openRepo(t, dir).CommitAllEnv("pinned commit", pinnedEnv())
	if err != nil || !committed {
		t.Fatalf("CommitAllEnv = %v, %v; want a commit", committed, err)
	}
	inProcess := mustRun(t, dir, "rev-parse", "HEAD")
	got := mustRun(t, dir, "log", "-1", "--date=raw", "--format=%an <%ae> %ad|%cn <%ce> %cd")
	want := "pinned author <author@example.invalid> 1767225600 +0000|pinned committer <committer@example.invalid> 1767225600 +0100"
	if got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}

	mustRun(t, dir, "reset", "-q", "--soft", "HEAD~1")
	if _, err := RunEnv(dir, pinnedEnv(), "commit", "-q", "-m", "pinned commit"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if cli := mustRun(t, dir, "rev-parse", "HEAD"); cli != inProcess {
		t.Fatalf("git program's commit = %s, CommitAllEnv's = %s; want the same hash", cli, inProcess)
	}
}

// TestCommitAllEnvDeclinesLikeCommitAll: a pinned date gitx cannot read, or
// an environment that leaves the identity to configuration, hands the commit
// to the git program with nothing staged.
func TestCommitAllEnvDeclinesLikeCommitAll(t *testing.T) {
	dir := newStoreRepo(t)
	writeFileT(t, dir, "b.txt", "b\n")
	head := mustRun(t, dir, "rev-parse", "HEAD")

	bad := append(pinnedEnv(), "GIT_AUTHOR_DATE=yesterday noon")
	if _, err := openRepo(t, dir).CommitAllEnv("unparsed", bad); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("CommitAllEnv with an unreadable date = %v, want ErrUseCLI", err)
	}
	if _, err := openRepo(t, dir).CommitAllEnv("no identity", []string{"GIT_AUTHOR_NAME=only"}); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("CommitAllEnv with no committer = %v, want ErrUseCLI", err)
	}
	if st := mustRun(t, dir, "status", "--porcelain"); st != "?? b.txt" {
		t.Fatalf("status after the declined commits = %q, want b.txt still unstaged", st)
	}
	if got := mustRun(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s, want %s", got, head)
	}
}
