package gitx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCommitAllMatchesTheGitProgram: the same changes made to two copies of
// a store, committed once in process and once by `git add -A && git
// commit`, give the same tree: changed, added and deleted files, a file
// replaced by a directory, a tracked file inside an ignored directory,
// ignore rules from the root, from a subdirectory and from info/exclude,
// matched case-insensitively under core.ignoreCase, and names that sort
// around "/".
func TestCommitAllMatchesTheGitProgram(t *testing.T) {
	base := newStoreRepo(t)
	writeFileT(t, base, ".gitignore", "*.lock\nvendor/\n")
	for name, body := range map[string]string{
		"a-b": "1\n", "a.b": "2\n", "a/x.txt": "3\n", "a0": "4\n",
		"deep/er/est.txt": "5\n", "gone/soon.txt": "6\n", "file-to-dir": "7\n",
		"with space.txt": "8\n",
	} {
		writeFileT(t, base, name, body)
	}
	mustRun(t, base, "add", "-A")
	writeFileT(t, base, "vendor/kept.txt", "tracked though ignored\n")
	mustRun(t, base, "add", "-f", "vendor/kept.txt")
	mustRun(t, base, "commit", "-q", "-m", "layout")

	change := func(t *testing.T, dir string) {
		t.Helper()
		writeFileT(t, dir, "a/x.txt", "3 changed\n")
		if err := os.RemoveAll(filepath.Join(dir, "gone")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "file-to-dir")); err != nil {
			t.Fatal(err)
		}
		writeFileT(t, dir, "file-to-dir/inner.txt", "now a directory\n")
		writeFileT(t, dir, "vendor/kept.txt", "tracked though ignored, changed\n")
		writeFileT(t, dir, "vendor/new.txt", "ignored\n")
		writeFileT(t, dir, "new/deeper/n.txt", "new\n")
		writeFileT(t, dir, "x.lock", "ignored\n")
		writeFileT(t, dir, "Journal.LOCK", "ignored under core.ignoreCase\n")
		writeFileT(t, dir, "deep/.gitignore", "*.tmp\n")
		writeFileT(t, dir, "deep/er/junk.tmp", "ignored below\n")
		writeFileT(t, dir, "deep/er/real.txt", "added below\n")
		writeFileT(t, dir, ".git/info/exclude", "excluded.txt\n")
		writeFileT(t, dir, "excluded.txt", "excluded\n")
		if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inProcess := filepath.Join(t.TempDir(), "in-process")
	program := filepath.Join(t.TempDir(), "program")
	mustRun(t, "", "clone", "-q", base, inProcess)
	mustRun(t, "", "clone", "-q", base, program)
	for _, dir := range []string{inProcess, program} {
		mustRun(t, dir, "config", "core.ignoreCase", "true")
		change(t, dir)
	}

	committed, err := openRepo(t, inProcess).CommitAll("jig: record", "jig", "jig@invalid")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v; want a commit", committed, err)
	}
	mustRun(t, program, "add", "-A")
	mustRun(t, program, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-q", "-m", "jig: record")

	got, want := mustRun(t, inProcess, "rev-parse", "HEAD^{tree}"), mustRun(t, program, "rev-parse", "HEAD^{tree}")
	if got != want {
		t.Fatalf("in-process tree %s, the git program's %s:\n%s\nwant:\n%s", got, want,
			mustRun(t, inProcess, "ls-tree", "-r", "HEAD"), mustRun(t, program, "ls-tree", "-r", "HEAD"))
	}
	if st := mustRun(t, inProcess, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("git status after CommitAll = %q, want clean", st)
	}
	if msg := mustRun(t, inProcess, "log", "-1", "--format=%an <%ae>|%s|%P"); msg != "jig <jig@invalid>|jig: record|"+mustRun(t, base, "rev-parse", "HEAD") {
		t.Fatalf("commit = %q", msg)
	}
	mustRun(t, inProcess, "fsck", "--strict")
}

// TestCommitAllNothingToCommit: a clean work tree, one with only ignored
// files, and one whose files were only touched give no commit; a touched
// file's new time is recorded, so the git program sees the tree clean.
func TestCommitAllNothingToCommit(t *testing.T) {
	dir := newStoreRepo(t)
	head := mustRun(t, dir, "rev-parse", "HEAD")
	commitAll := func(when string) {
		t.Helper()
		committed, err := openRepo(t, dir).CommitAll("nothing", "jig", "jig@invalid")
		if err != nil || committed {
			t.Fatalf("CommitAll %s = %v, %v; want no commit", when, committed, err)
		}
	}
	commitAll("on a clean tree")
	writeFileT(t, dir, "journal.lock", "held")
	commitAll("with only an ignored file")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "project.yaml"), later, later); err != nil {
		t.Fatal(err)
	}
	commitAll("with a touched file")
	if got := mustRun(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	idx, _, err := readIndex(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx.Entries {
		if e.Name == "project.yaml" && !e.ModifiedAt.Equal(later) {
			t.Fatalf("project.yaml's index time = %v, want the touched %v", e.ModifiedAt, later)
		}
	}
	if st := mustRun(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("git status = %q, want clean", st)
	}
}

// TestCommitAllCommitsTheEmptyTree: with every tracked file gone, the
// commit records git's empty tree, as `git add -A && git commit` does.
func TestCommitAllCommitsTheEmptyTree(t *testing.T) {
	dir := newStoreRepo(t)
	r := openRepo(t, dir)
	for _, name := range []string{".gitattributes", ".gitignore", "project.yaml"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := r.CommitAll("empty", "jig", "jig@invalid")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v; want a commit", committed, err)
	}
	if got := mustRun(t, dir, "rev-parse", "HEAD^{tree}"); got != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("tree = %s, want the empty tree", got)
	}
	mustRun(t, dir, "fsck", "--strict")
}

// TestCommitAllCatchesARacilyCleanFile: a file whose size and time still
// match its index entry, but whose time is not before the index file's, may
// have changed after the index was written; it is read again, as the git
// program reads it, and its change committed.
func TestCommitAllCatchesARacilyCleanFile(t *testing.T) {
	dir := newStoreRepo(t)
	if _, err := openRepo(t, dir).CommitAll("refresh", "jig", "jig@invalid"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "project.yaml")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, "project.yaml", "schema_version: 9\n") // the same size
	for _, p := range []string{path, filepath.Join(dir, ".git", "index")} {
		if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := openRepo(t, dir).CommitAll("racy", "jig", "jig@invalid")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v; want the racily clean change committed", committed, err)
	}
	if got := mustRun(t, dir, "show", "HEAD:project.yaml"); got != "schema_version: 9" {
		t.Fatalf("committed project.yaml = %q", got)
	}
}

// TestCommitAllOnAnUnbornBranch: the first commit has no parent, and an
// unborn branch with nothing in its work tree gets none.
func TestCommitAllOnAnUnbornBranch(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q", "-b", "trunk")
	// Nothing in the work tree, not even the attributes OpenRepo asks for.
	if committed, err := (&Repo{dir: dir}).CommitAll("empty", "jig", "jig@invalid"); err != nil || committed {
		t.Fatalf("CommitAll on an empty unborn branch = %v, %v; want no commit", committed, err)
	}
	writeFileT(t, dir, ".gitattributes", StoreAttributes)
	writeFileT(t, dir, "project.yaml", "schema_version: 1\n")
	committed, err := openRepo(t, dir).CommitAll("first", "jig", "jig@invalid")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v; want the first commit", committed, err)
	}
	if got := mustRun(t, dir, "log", "--format=%s|%P", "trunk"); got != "first|" {
		t.Fatalf("trunk = %q, want one commit with no parent", got)
	}
	mustRun(t, dir, "fsck", "--strict")
}

// TestCommitAllLeavesWhatTheGitProgramStagesDifferently: a work tree or
// index the git program would stage differently from a plain file's bytes
// hands the commit to it, with nothing changed and no lock left.
func TestCommitAllLeavesWhatTheGitProgramStagesDifferently(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
	}{
		{"a symbolic link", func(t *testing.T, dir string) {
			if err := os.Symlink("project.yaml", filepath.Join(dir, "link")); err != nil {
				t.Skipf("no symbolic links here: %v", err)
			}
		}},
		{"an executable where core.fileMode counts it", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.fileMode", "true")
			writeFileT(t, dir, "run.sh", "#!/bin/sh\n")
			if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
			if fi, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil || fi.Mode()&0o111 == 0 {
				t.Skip("no executable bit here")
			}
		}},
		{"an executable in the index", func(t *testing.T, dir string) {
			writeFileT(t, dir, "run.sh", "#!/bin/sh\n")
			blob := mustRun(t, dir, "hash-object", "-w", "run.sh")
			mustRun(t, dir, "update-index", "--add", "--cacheinfo", "100755,"+blob+",run.sh")
		}},
		{"an embedded repository", func(t *testing.T, dir string) {
			mustRun(t, dir, "init", "-q", filepath.Join(dir, "inner"))
		}},
		{"a .gitattributes below the root", func(t *testing.T, dir string) {
			writeFileT(t, dir, "T-1/.gitattributes", "* text\n")
		}},
		{"a name core.precomposeUnicode would recompose", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.precomposeUnicode", "true")
			writeFileT(t, dir, "caf\u00e9.txt", "x\n")
		}},
		{"an ignore rule with a negation", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitignore", "*.lock\n!keep.lock\n")
		}},
		{"an ignore rule with **", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitignore", "cache/**\n")
		}},
		{"an ignore rule with a bracket expression", func(t *testing.T, dir string) {
			writeFileT(t, dir, "T-1/.gitignore", "[Jj]ournal\n")
		}},
		{"an ignore rule with an escape", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/info/exclude", "\\#hash\n")
		}},
		{"an ignore rule with trailing whitespace", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitignore", "*.tmp \n")
		}},
		{"an assume-unchanged file", func(t *testing.T, dir string) {
			mustRun(t, dir, "update-index", "--assume-unchanged", "project.yaml")
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
		}},
		{"an assume-unchanged file in a version 4 index", func(t *testing.T, dir string) {
			mustRun(t, dir, "update-index", "--index-version", "4")
			mustRun(t, dir, "update-index", "--assume-unchanged", "project.yaml")
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
		}},
		{"a file dated before 1970", func(t *testing.T, dir string) {
			writeFileT(t, dir, "old.txt", "old\n")
			when := time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := os.Chtimes(filepath.Join(dir, "old.txt"), when, when); err != nil {
				t.Skipf("no such time here: %v", err)
			}
		}},
		{"two tracked names core.ignoreCase makes one", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.ignoreCase", "true")
			blob := mustRun(t, dir, "hash-object", "-w", "project.yaml")
			mustRun(t, dir, "update-index", "--add", "--cacheinfo", "100644,"+blob+",NOTES.txt")
			mustRun(t, dir, "update-index", "--add", "--cacheinfo", "100644,"+blob+",notes.txt")
			writeFileT(t, dir, "notes.txt", "schema_version: 1\n")
		}},
		{"a tracked name another spelling still finds", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.ignoreCase", "false")
			if err := os.Rename(filepath.Join(dir, "project.yaml"), filepath.Join(dir, "p.tmp")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, "p.tmp"), filepath.Join(dir, "PROJECT.yaml")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "project.yaml")); err != nil {
				t.Skip("a case-sensitive file system: the old name is gone")
			}
		}},
		{"a name core.ignoreCase matches to a tracked one", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.ignoreCase", "true")
			writeFileT(t, dir, "PROJECT.yaml", "schema_version: 1\n")
			if ents, err := os.ReadDir(dir); err != nil || len(ents) < 5 {
				t.Skip("a case-insensitive file system: the two names are one file")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			head := mustRun(t, dir, "rev-parse", "HEAD")
			c.lay(t, dir)
			if _, err := openRepo(t, dir).CommitAll("x", "jig", "jig@invalid"); !errors.Is(err, ErrUseCLI) {
				t.Fatalf("CommitAll = %v, want ErrUseCLI", err)
			}
			if got := mustRun(t, dir, "rev-parse", "HEAD"); got != head {
				t.Fatalf("HEAD moved to %s", got)
			}
			for _, lock := range []string{"index.lock", "refs/heads/main.lock"} {
				if _, err := os.Stat(filepath.Join(dir, ".git", filepath.FromSlash(lock))); !os.IsNotExist(err) {
					t.Fatalf("%s left behind: %v", lock, err)
				}
			}
		})
	}
}

// TestCommitAllRecordsAnExecutableAsAFileWhereCoreFileModeIsOff: where the
// repository's core.fileMode is false (Git for Windows, and file systems
// that report every file executable), the executable bit is not recorded,
// as the git program does not record it.
func TestCommitAllRecordsAnExecutableAsAFileWhereCoreFileModeIsOff(t *testing.T) {
	dir := newStoreRepo(t)
	mustRun(t, dir, "config", "core.fileMode", "false")
	writeFileT(t, dir, "run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := openRepo(t, dir).CommitAll("run", "jig", "jig@invalid"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if got := mustRun(t, dir, "ls-tree", "HEAD", "run.sh"); got[:6] != "100644" {
		t.Fatalf("run.sh = %q, want a plain file", got)
	}
}

// TestCommitAllHonorsTheIdentityEnvironment: GIT_AUTHOR_* and
// GIT_COMMITTER_* win over the identity passed in, as they do for the git
// program, and a pinned date gitx cannot read hands the commit to the git
// program before anything is staged.
func TestCommitAllHonorsTheIdentityEnvironment(t *testing.T) {
	dir := newStoreRepo(t)
	t.Setenv("GIT_AUTHOR_NAME", "pinned author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.invalid")
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_NAME", "pinned committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.invalid")
	t.Setenv("GIT_COMMITTER_DATE", "1767225600 +0100")
	writeFileT(t, dir, "a.txt", "a\n")
	if _, err := openRepo(t, dir).CommitAll("pinned", "jig", "jig@invalid"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	got := mustRun(t, dir, "log", "-1", "--date=raw", "--format=%an <%ae> %ad|%cn <%ce> %cd")
	want := "pinned author <author@example.invalid> 1767225600 +0000|pinned committer <committer@example.invalid> 1767225600 +0100"
	if got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}

	t.Setenv("GIT_AUTHOR_DATE", "yesterday noon")
	writeFileT(t, dir, "b.txt", "b\n")
	if _, err := openRepo(t, dir).CommitAll("unparsed", "jig", "jig@invalid"); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("CommitAll with an unreadable date = %v, want ErrUseCLI", err)
	}
	if st := mustRun(t, dir, "status", "--porcelain"); st != "?? b.txt" {
		t.Fatalf("status after the refused commit = %q, want b.txt still unstaged", st)
	}
}

// TestParseGitDate covers the date forms signature reads.
func TestParseGitDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", true},
		{"2026-01-01T02:00:00+02:00", "2026-01-01T02:00:00+02:00", true},
		{"1767225600 +0000", "2026-01-01T00:00:00Z", true},
		{"@1767225600 -0530", "2025-12-31T18:30:00-05:30", true},
		{"yesterday", "", false},
		{"1767225600", "", false},
		{"1767225600 +00", "", false},
	}
	for _, c := range cases {
		got, ok := parseGitDate(c.in)
		if ok != c.ok {
			t.Errorf("parseGitDate(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got.Format(time.RFC3339) != c.want {
			t.Errorf("parseGitDate(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want)
		}
	}
}

// TestCommitAllRecordsWhatTheGitProgramCompares: the stat data CommitAll
// records for a file it hashed (change time, device, inode, owner, where the
// platform has them) is what the git program compares, so a `git status`
// afterwards finds the file unchanged without reading it and rewriting the
// index.
func TestCommitAllRecordsWhatTheGitProgramCompares(t *testing.T) {
	dir := newStoreRepo(t)
	writeFileT(t, dir, "T-1/journal.ndjson", "{}\n")
	writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
	if _, err := openRepo(t, dir).CommitAll("record", "jig", "jig@invalid"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	// Past the index's own time, so no entry is racily clean for git.
	time.Sleep(50 * time.Millisecond)
	path := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st := mustRun(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("git status = %q, want clean", st)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("git status rewrote the index: the stat data CommitAll recorded is not what git compares")
	}
}
