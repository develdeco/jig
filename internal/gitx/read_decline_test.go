package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// envOf is an environment of its own for the reads to look at.
func envOf(vars map[string]string) envLookup {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

const plainConfig = "[core]\n\trepositoryformatversion = 0\n\tbare = false\n"

// fakeRepo writes a repository by hand, the files git needs to take a
// directory for one and no more: HEAD at a branch with a ref (the commit it
// names is not an object), objects, refs and a config.
func fakeRepo(t *testing.T) (work, gitDir string) {
	t.Helper()
	work = t.TempDir()
	gitDir = filepath.Join(work, ".git")
	for _, d := range []string{"objects", "refs/heads"} {
		if err := os.MkdirAll(filepath.Join(gitDir, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(gitDir, "config"), plainConfig)
	writeFile(t, filepath.Join(gitDir, "refs", "heads", "main"), "0123456789012345678901234567890123456789\n")
	return work, gitDir
}

// TestDiscoverDeclinesWhatItDoesNotDoLikeGit is one case per decline rule of
// discover: the plain repository is answered, and each way it departs from
// the plain case is left to the git program.
func TestDiscoverDeclinesWhatItDoesNotDoLikeGit(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, work, gitDir string)
		start  string // relative to work; "" for work itself
		env    map[string]string
		want   bool
	}{
		{name: "the plain case", want: true},
		{name: "a subdirectory", start: "a/b", want: true, mutate: func(t *testing.T, work, _ string) {
			mkdir(t, filepath.Join(work, "a", "b"))
		}},
		{name: "core.bare false", want: true, mutate: setConfig(plainConfig)},
		{name: "no config", want: true, mutate: func(t *testing.T, _, gitDir string) { os.Remove(filepath.Join(gitDir, "config")) }},
		{name: "repository format 1", want: true, mutate: setConfig("[core]\n\trepositoryformatversion = 1\n")},
		{name: "core.bare true", mutate: setConfig("[core]\n\trepositoryformatversion = 0\n\tbare = true\n")},
		{name: "core.bare with no value", mutate: setConfig("[core]\n\trepositoryformatversion = 0\n\tbare\n")},
		{name: "core.worktree", mutate: setConfig(plainConfig + "\tworktree = ../elsewhere\n")},
		{name: "an extension", mutate: setConfig("[core]\n\trepositoryformatversion = 1\n[extensions]\n\tobjectformat = sha256\n")},
		{name: "an extension in version 0", mutate: setConfig(plainConfig + "[extensions]\n\tworktreeConfig = true\n")},
		{name: "repository format 2", mutate: setConfig("[core]\n\trepositoryformatversion = 2\n")},
		{name: "an include", mutate: setConfig(plainConfig + "[include]\n\tpath = other\n")},
		{name: "a conditional include", mutate: setConfig(plainConfig + "[includeIf \"gitdir:/x/\"]\n\tpath = other\n")},
		{name: "a config that does not parse", mutate: setConfig("[core\n\tbare = false\n")},
		{name: "GIT_CEILING_DIRECTORIES", env: map[string]string{"GIT_CEILING_DIRECTORIES": "/"}},
		{name: "GIT_CEILING_DIRECTORIES empty", env: map[string]string{"GIT_CEILING_DIRECTORIES": ""}},
		{name: "GIT_DISCOVERY_ACROSS_FILESYSTEM", env: map[string]string{"GIT_DISCOVERY_ACROSS_FILESYSTEM": "1"}},
		{name: "GIT_NAMESPACE", env: map[string]string{"GIT_NAMESPACE": "ns"}},
		{name: "GIT_REPLACE_REF_BASE", env: map[string]string{"GIT_REPLACE_REF_BASE": "refs/other/"}},
		{name: "GIT_GRAFT_FILE", env: map[string]string{"GIT_GRAFT_FILE": "grafts"}},
		{name: "GIT_SHALLOW_FILE", env: map[string]string{"GIT_SHALLOW_FILE": "shallow"}},
		{name: "starting in the git directory", start: ".git"},
		{name: "starting below the git directory", start: ".git/refs/heads"},
		{name: "starting in a directory named .git", start: "x/.git", mutate: func(t *testing.T, work, _ string) {
			mkdir(t, filepath.Join(work, "x", ".git"))
		}},
		{name: "a HEAD git would not accept", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "HEAD"), "garbage\n")
		}},
		{name: "a HEAD outside refs", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: heads/main\n")
		}},
		{name: "no objects", mutate: func(t *testing.T, _, gitDir string) { os.RemoveAll(filepath.Join(gitDir, "objects")) }},
		{name: "no refs", mutate: func(t *testing.T, _, gitDir string) { os.RemoveAll(filepath.Join(gitDir, "refs")) }},
		{name: "an empty .git directory", mutate: func(t *testing.T, _, gitDir string) {
			os.RemoveAll(gitDir)
			mkdir(t, gitDir)
		}},
		{name: "a .git file that is not a gitdir line", mutate: replaceWithGitFile("not a gitfile\n")},
		{name: "a .git file with nothing after gitdir:", mutate: replaceWithGitFile("gitdir: \n")},
		{name: "a .git file naming nothing", mutate: replaceWithGitFile("gitdir: nowhere/at/all\n")},
		{name: "a commondir file naming nothing", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "commondir"), "../nowhere\n")
		}},
		{name: "an empty commondir file", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "commondir"), "\n")
		}},
		{name: "a bare repository", start: "", mutate: func(t *testing.T, work, gitDir string) {
			// The git directory's files are the directory's own: no .git.
			tmp := work + ".bare"
			if err := os.Rename(gitDir, tmp); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(work); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, work); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, gitDir := fakeRepo(t)
			if c.mutate != nil {
				c.mutate(t, work, gitDir)
			}
			start := work
			if c.start != "" {
				start = filepath.Join(work, filepath.FromSlash(c.start))
				if _, err := os.Stat(start); err != nil {
					mkdir(t, start)
				}
			}
			loc, ok := discover(start, envOf(c.env))
			if ok != c.want {
				t.Fatalf("discover answered = %v, want %v", ok, c.want)
			}
			if ok && canonPath(loc.workTree) != canonPath(work) {
				t.Errorf("work tree = %s, want %s", loc.workTree, work)
			}
		})
	}
	t.Run("a .git symbolic link", func(t *testing.T) {
		work, gitDir := fakeRepo(t)
		moved := work + ".git-target"
		if err := os.Rename(gitDir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, gitDir); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		if _, ok := discover(work, os.LookupEnv); ok {
			t.Errorf("answered for a .git that is a symbolic link")
		}
	})
	t.Run("a HEAD that is a symbolic link", func(t *testing.T) {
		work, gitDir := fakeRepo(t)
		head := filepath.Join(gitDir, "HEAD")
		if err := os.Remove(head); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("refs", "heads", "main"), head); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
		if _, ok := discover(work, os.LookupEnv); ok {
			t.Errorf("answered for a HEAD that is a symbolic link")
		}
	})
	t.Run("a directory reached through a junction", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("directory junctions only exist on Windows")
		}
		work, _ := fakeRepo(t)
		mkdir(t, filepath.Join(work, "sub"))
		junction := filepath.Join(t.TempDir(), "via-junction")
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, work).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v\n%s", err, out)
		}
		// Removed before the temp dir holding it, so the cleanup never walks
		// through the junction into the repository.
		t.Cleanup(func() { os.Remove(junction) })
		for _, dir := range []string{junction, filepath.Join(junction, "sub")} {
			if _, ok := discover(dir, os.LookupEnv); ok {
				t.Errorf("discover(%s) answered; git resolves the junction and EvalSymlinks does not", dir)
			}
		}
		// git prints the target's path, which is what TopLevel still returns.
		top, err := TopLevel(filepath.Join(junction, "sub"))
		if err != nil || canonPath(top) != canonPath(work) {
			t.Errorf("TopLevel through a junction = %q, %v; want %s", top, err, work)
		}
	})
	t.Run("no repository", func(t *testing.T) {
		if loc, ok := discover(t.TempDir(), os.LookupEnv); ok {
			t.Errorf("answered outside a repository: %+v", loc)
		}
		if _, ok := discover("", os.LookupEnv); ok {
			t.Errorf("answered for no directory")
		}
		if _, ok := discover(filepath.Join(t.TempDir(), "missing"), os.LookupEnv); ok {
			t.Errorf("answered for a directory that is not there")
		}
	})
	t.Run("the exported functions still answer through git", func(t *testing.T) {
		work, gitDir := fakeRepo(t)
		setConfig("[core]\n\trepositoryformatversion = 0\n\tbare = true\n")(t, work, gitDir)
		if _, err := TopLevel(work); err == nil {
			t.Errorf("TopLevel of a bare repository succeeded")
		}
	})
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func setConfig(content string) func(t *testing.T, work, gitDir string) {
	return func(t *testing.T, _, gitDir string) { writeFile(t, filepath.Join(gitDir, "config"), content) }
}

// replaceWithGitFile makes .git a file with this content.
func replaceWithGitFile(content string) func(t *testing.T, work, gitDir string) {
	return func(t *testing.T, _, gitDir string) {
		if err := os.RemoveAll(gitDir); err != nil {
			t.Fatal(err)
		}
		writeFile(t, gitDir, content)
	}
}

// TestObjectReadsDeclineWhatGitReadsDifferently is one case per rule that
// leaves a repository's objects to the git program while its refs are still
// read: plain refs resolve, an object read does not.
func TestObjectReadsDeclineWhatGitReadsDifferently(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	cases := []struct {
		name   string
		mutate func(t *testing.T, work, gitDir string)
		want   bool // objects are read in process
	}{
		{name: "the plain case", want: true},
		{name: "alternates", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "objects", "info", "alternates"), "/elsewhere/objects\n")
		}},
		{name: "grafts", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "info", "grafts"), sha+"\n")
		}},
		{name: "a shallow history", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "shallow"), sha+"\n")
		}},
		{name: "a loose replace ref", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "refs", "replace", sha), sha+"\n")
		}},
		{name: "an empty replace directory", want: true, mutate: func(t *testing.T, _, gitDir string) {
			mkdir(t, filepath.Join(gitDir, "refs", "replace"))
		}},
		{name: "a packed replace ref", mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "packed-refs"), "# pack-refs with: peeled fully-peeled sorted \n"+sha+" refs/replace/"+sha+"\n")
		}},
		{name: "other packed refs", want: true, mutate: func(t *testing.T, _, gitDir string) {
			writeFile(t, filepath.Join(gitDir, "packed-refs"), "# pack-refs with: peeled fully-peeled sorted \n"+sha+" refs/tags/x\n")
		}},
		{name: "packed-refs too big to scan", mutate: func(t *testing.T, _, gitDir string) {
			f, err := os.Create(filepath.Join(gitDir, "packed-refs"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(maxPackedRefs + 1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pack indexes of a size that is read", want: true, mutate: func(t *testing.T, _, gitDir string) {
			fakeIndex(t, filepath.Join(gitDir, "objects", "pack", "pack-a.idx"), maxPackIndexBytes)
		}},
		{name: "pack indexes of more than 16 MB", mutate: func(t *testing.T, _, gitDir string) {
			fakeIndex(t, filepath.Join(gitDir, "objects", "pack", "pack-a.idx"), maxPackIndexBytes/2)
			fakeIndex(t, filepath.Join(gitDir, "objects", "pack", "pack-b.idx"), maxPackIndexBytes/2+1)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, gitDir := fakeRepo(t)
			if c.mutate != nil {
				c.mutate(t, work, gitDir)
			}
			r := newReader(work, os.LookupEnv)
			if r == nil {
				t.Fatal("the repository itself is declined")
			}
			defer r.close()
			if _, ok := r.objects(); ok != c.want {
				t.Errorf("objects read in process = %v, want %v", ok, c.want)
			}
			wantRes := resDecline
			if c.want {
				wantRes = resNoObject // the commit HEAD names is not an object
			}
			if _, res := r.rev(mustParse(t, "HEAD^{commit}")); res != wantRes {
				t.Errorf("HEAD^{commit} = %d, want %d", res, wantRes)
			}
			if c.name == "packed-refs too big to scan" {
				return // the plain ref is a loose file, and still read
			}
			if sha2, res := r.rev(mustParse(t, "HEAD")); res != resFound || sha2 != sha {
				t.Errorf("HEAD = %q, %d; a ref is read whatever the objects are", sha2, res)
			}
		})
	}
}

func mustParse(t *testing.T, rev string) revSpec {
	t.Helper()
	spec, ok := parseRev(rev)
	if !ok {
		t.Fatalf("parseRev(%q) declined", rev)
	}
	return spec
}

// fakeIndex writes a file of size bytes where a pack index would be.
func fakeIndex(t *testing.T, path string, size int64) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

// TestRefReadsDeclineWhatTheyDoNotReadLikeGit covers the ref files the reads
// leave to git.
func TestRefReadsDeclineWhatTheyDoNotReadLikeGit(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	for _, c := range []struct {
		name, content string
		want          result
	}{
		{"an object name", sha + "\n", resFound},
		{"an object name with trailing text after white space", sha + " trailing\n", resFound},
		{"an object name with no newline", sha, resFound},
		{"an object name in capitals", strings.ToUpper(sha) + "\n", resFound},
		{"an object name run into other text", sha + "x\n", resDecline},
		{"a short object name", sha[:39] + "\n", resDecline},
		{"an empty file", "", resDecline},
		{"a symbolic ref to a missing ref", "ref: refs/heads/nothere\n", resNoRef},
		{"a symbolic ref with a bad name", "ref: nothere\n", resDecline},
		{"a symbolic ref to a name with a space", "ref: refs/heads/a b\n", resDecline},
		{"a vertical tab is not white space to git", sha + "\v\n", resDecline},
	} {
		t.Run(c.name, func(t *testing.T) {
			work, gitDir := fakeRepo(t)
			writeFile(t, filepath.Join(gitDir, "refs", "heads", "x"), c.content)
			r := newReader(work, os.LookupEnv)
			defer r.close()
			if _, res := r.rev(mustParse(t, "refs/heads/x")); res != c.want {
				t.Errorf("result %d, want %d", res, c.want)
			}
		})
	}
	t.Run("a directory where the ref file would be", func(t *testing.T) {
		work, gitDir := fakeRepo(t)
		writeFile(t, filepath.Join(gitDir, "refs", "heads", "d", "e"), sha+"\n")
		r := newReader(work, os.LookupEnv)
		defer r.close()
		if _, res := r.rev(mustParse(t, "refs/heads/d")); res != resNoRef {
			t.Errorf("refs/heads/d = %d, want no ref", res)
		}
		if got, res := r.rev(mustParse(t, "refs/heads/d/e")); res != resFound || got != sha {
			t.Errorf("refs/heads/d/e = %q, %d", got, res)
		}
	})
	t.Run("a name another rule of git's would find", func(t *testing.T) {
		// refs/heads/refs/heads/x is what git's own short-name rules find for
		// the name refs/heads/x when no ref has that name.
		work, gitDir := fakeRepo(t)
		writeFile(t, filepath.Join(gitDir, "refs", "heads", "refs", "heads", "x"), sha+"\n")
		r := newReader(work, os.LookupEnv)
		defer r.close()
		if _, res := r.rev(mustParse(t, "refs/heads/x")); res != resDecline {
			t.Errorf("refs/heads/x = %d, want left to git", res)
		}
		writeFile(t, filepath.Join(gitDir, "packed-refs"), sha+" refs/tags/refs/heads/y\n")
		if _, res := r.rev(mustParse(t, "refs/heads/y")); res != resDecline {
			t.Errorf("refs/heads/y = %d, want left to git (a packed ref under another rule)", res)
		}
	})
}

// hidingStore hides one object from the reads, as a repack running at the
// time can: a one-shot read finds nothing where the git program, which
// retries, finds it.
type hidingStore struct {
	objectStore
	hide plumbing.Hash
}

func (h hidingStore) HasEncodedObject(x plumbing.Hash) error {
	if x == h.hide {
		return plumbing.ErrObjectNotFound
	}
	return h.objectStore.HasEncodedObject(x)
}

func (h hidingStore) EncodedObject(typ plumbing.ObjectType, x plumbing.Hash) (plumbing.EncodedObject, error) {
	if x == h.hide {
		return nil, plumbing.ErrObjectNotFound
	}
	return h.objectStore.EncodedObject(typ, x)
}

// TestNegativeObjectAnswersAreConfirmedByGit hides a commit from the
// in-process reads: they answer "not there", and Resolve asks git before it
// says so, which finds it. What git agrees is missing stays missing.
func TestNegativeObjectAnswersAreConfirmedByGit(t *testing.T) {
	dir := initFixtureRepo(t, t.TempDir(), "confirm")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	mustGit(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	head := mustGit(t, dir, "rev-parse", "HEAD")
	parent := mustGit(t, dir, "rev-parse", "HEAD^")

	r := newReader(dir, os.LookupEnv)
	defer r.close()
	db, ok := r.objects()
	if !ok {
		t.Fatal("objects are not read in process")
	}
	r.db = &objectDB{st: hidingStore{objectStore: db.st, hide: plumbing.NewHash(head)}}

	if _, res := r.rev(mustParse(t, head+"^{commit}")); res != resNoObject {
		t.Fatalf("the hidden commit = %d, want not found", res)
	}
	if sha, ok, err := resolveWith(dir, head+"^{commit}", r, false); err != nil || !ok || sha != head {
		t.Errorf("resolveWith(hidden commit) = %q, %v, %v; git has it", sha, ok, err)
	}
	if sha, ok, err := resolveWith(dir, "HEAD^{commit}", r, false); err != nil || !ok || sha != head {
		t.Errorf("resolveWith(HEAD^{commit}) = %q, %v, %v; git has it", sha, ok, err)
	}
	if sha, ok, err := resolveWith(dir, parent+"^{commit}", r, false); err != nil || !ok || sha != parent {
		t.Errorf("resolveWith(visible commit) = %q, %v, %v", sha, ok, err)
	}
	if sha, ok, err := resolveWith(dir, "0123456789012345678901234567890123456789^{commit}", r, false); err != nil || ok {
		t.Errorf("resolveWith(missing commit) = %q, %v, %v; git agrees it is missing", sha, ok, err)
	}
	if has := hasPathAt(dir, head, "x", os.LookupEnv); has {
		t.Errorf("hasPathAt of a path that is not there")
	}
	// Missing finds the hidden commit through the same confirmation.
	if missing, err := Missing(dir, "HEAD", []string{head, "0123456789012345678901234567890123456789"}); err != nil || len(missing) != 1 {
		t.Errorf("Missing = %v, %v", missing, err)
	}
}
