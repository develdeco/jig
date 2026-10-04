package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteMemorizeCommitsOnlyTheRetrievalNotes: the memorize commit holds the
// retrieval notes and nothing else. By the time publish writes it, the
// publish lease may have run every oracle (when the target moved), and an
// oracle can leave anything behind: a file git does not ignore, a rewrite of a
// tracked file, a change it staged. None of that belongs in a commit that is
// pushed to a branch its author built, so the lease is put back at its head
// before the notes are written, and the notes are the only change the commit
// makes. A second call, with the notes unchanged, makes no second commit.
func TestWriteMemorizeCommitsOnlyTheRetrievalNotes(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	for name, content := range map[string]string{
		"stray.txt":  "left behind by an oracle\n",
		"f.txt":      "rewritten by a formatter\n", // tracked since the repo's first commit
		"staged.txt": "staged by an oracle\n",
	} {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run(t, clone, "add", "staged.txt")
	before := run(t, clone, "rev-parse", "HEAD")

	if err := writeMemorize(clone, "T-1", nil, nil, nil, buildGitEnv); err != nil {
		t.Fatalf("writeMemorize: %v", err)
	}

	if got := run(t, clone, "rev-parse", "HEAD~1"); got != before {
		t.Fatalf("the memorize commit's parent is %s, want the head it was made on, %s", got, before)
	}
	if got := run(t, clone, "log", "-1", "--format=%s"); got != "docs: memorize T-1" {
		t.Errorf("the commit's subject is %q, want %q", got, "docs: memorize T-1")
	}
	if got := run(t, clone, "show", "--name-only", "--format=", "HEAD"); got != ".claude/retrieval/T-1.md" {
		t.Errorf("the memorize commit changes %q, want only the retrieval notes", got)
	}
	if status := run(t, clone, "status", "--porcelain"); status != "" {
		t.Errorf("git status after the memorize commit:\n%s\nwant a lease with nothing left over from the oracles", status)
	}

	head := run(t, clone, "rev-parse", "HEAD")
	if err := writeMemorize(clone, "T-1", nil, nil, nil, buildGitEnv); err != nil {
		t.Fatalf("writeMemorize again: %v", err)
	}
	if got := run(t, clone, "rev-parse", "HEAD"); got != head {
		t.Errorf("writing unchanged notes made a commit: head %s, was %s", got, head)
	}
}
