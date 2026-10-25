package home

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDigesterPathsDeriveFromTheRootGiven: the ledger, hooks, logs and pull
// request cache paths, like every home-anchored path, come from the root passed
// in.
func TestDigesterPathsDeriveFromTheRootGiven(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if got, want := LedgerPath(root), filepath.Join(root, "ledger", "ledger.ndjson"); got != want {
		t.Errorf("LedgerPath = %q, want %q", got, want)
	}
	if got, want := HooksDir(root), filepath.Join(root, "hooks"); got != want {
		t.Errorf("HooksDir = %q, want %q", got, want)
	}
	if got, want := LogsDir(root), filepath.Join(root, "logs"); got != want {
		t.Errorf("LogsDir = %q, want %q", got, want)
	}
	if got, want := PullRequestStatesPath(root), filepath.Join(root, "cache", "pull-requests.json"); got != want {
		t.Errorf("PullRequestStatesPath = %q, want %q", got, want)
	}
}

// TestVerifyPaths: a verify's worktree and its log are named by the store, the
// ticket, the first twelve digits of the sha and the slice, side by side in
// two trees under the root.
func TestVerifyPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const sha = "0123456789abcdef0123456789abcdef01234567"

	wt, err := VerifyWorktreeDir(root, "0123456789abcdef", "JIG-1", sha, "slice-a")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "verify", "0123456789abcdef", "JIG-1", "0123456789ab-slice-a"); wt != want {
		t.Errorf("VerifyWorktreeDir = %q, want %q", wt, want)
	}
	log, err := VerifyLogPath(root, "0123456789abcdef", "JIG-1", sha, "slice-a")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "logs", "verify", "0123456789abcdef", "JIG-1", "0123456789ab-slice-a.log"); log != want {
		t.Errorf("VerifyLogPath = %q, want %q", log, want)
	}

	// A short sha is used whole, and two slices of one commit get two paths.
	short, err := VerifyWorktreeDir(root, "s", "JIG-1", "abc", "a")
	if err != nil || filepath.Base(short) != "abc-a" {
		t.Errorf("VerifyWorktreeDir with a short sha = %q, %v; want abc-a", short, err)
	}
	other, _ := VerifyWorktreeDir(root, "0123456789abcdef", "JIG-1", sha, "slice-b")
	if other == wt {
		t.Error("two slices of one commit share a worktree directory")
	}
}

// TestVerifyPathsRefuseWhatClimbsOut: each part must be one plain directory
// name, as the evidence tree's are.
func TestVerifyPathsRefuseWhatClimbsOut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	good := [4]string{"store", "JIG-1", "abc123def456", "a"}
	for i, what := range []string{"store id", "ticket", "sha", "slice"} {
		for _, bad := range []string{"", "..", ".hidden", "a/b", `a\b`, "c:x", "trail."} {
			parts := good
			parts[i] = bad
			if p, err := VerifyWorktreeDir(root, parts[0], parts[1], parts[2], parts[3]); err == nil {
				t.Errorf("VerifyWorktreeDir with %s %q = %q, want a refusal", what, bad, p)
			} else if !strings.Contains(err.Error(), what) {
				t.Errorf("refusal for %s %q = %q, want it to name the part", what, bad, err)
			}
			if p, err := VerifyLogPath(root, parts[0], parts[1], parts[2], parts[3]); err == nil {
				t.Errorf("VerifyLogPath with %s %q = %q, want a refusal", what, bad, p)
			}
		}
	}
}
