package fileshare_test

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
)

// git runs the git program in dir, with an identity for its commits.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, append([]string{"-c", "user.name=fileshare-test", "-c", "user.email=test@example.invalid"}, args...)...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// TestGitReplacesAndRemovesARefHeldOpen holds two ref files open with Open
// while git moves a new value into place over one and deletes the other, as
// a user's git does beside a jig that reads the repository: both go ahead,
// and the holder still reads the bytes it opened. Held with os.Open on
// Windows, git fails both after its retries.
func TestGitReplacesAndRemovesARefHeldOpen(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dir, "branch", "side")
	two, one := git(t, dir, "rev-parse", "HEAD"), git(t, dir, "rev-parse", "HEAD^")
	main := filepath.Join(dir, ".git", "refs", "heads", "main")
	held, err := fileshare.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	side, err := fileshare.Open(filepath.Join(dir, ".git", "refs", "heads", "side"))
	if err != nil {
		t.Fatal(err)
	}
	defer side.Close()

	git(t, dir, "update-ref", "refs/heads/main", one)
	git(t, dir, "branch", "-D", "side")

	if got, err := fileshare.ReadFile(main); err != nil || strings.TrimSpace(string(got)) != one {
		t.Errorf("ReadFile after the update = %q, %v, want %s", got, err, one)
	}
	if got, err := io.ReadAll(held); err != nil || strings.TrimSpace(string(got)) != two {
		t.Errorf("the holder read %q, %v, want the %s it opened", got, err, two)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "side")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the deleted branch's file: %v, want it gone", err)
	}
}

// TestAMissingFileIsNotExist checks that a missing file and a missing
// directory fail the way os.Open fails them, so callers tell absence apart.
func TestAMissingFileIsNotExist(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "none"), filepath.Join(dir, "no", "such")} {
		if _, err := fileshare.ReadFile(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%s) = %v, want fs.ErrNotExist", p, err)
		}
	}
}

// TestReplaceRidesOutAnotherProcessHoldingTheFile: another process holds the
// file open the way os.Open does on Windows (no delete sharing). The bare
// Rename over it fails with an error Replacing reports (Windows only); Replace
// returns nil once the holder lets go within the window, with the new bytes in
// place.
func TestReplaceRidesOutAnotherProcessHoldingTheFile(t *testing.T) {
	if held := os.Getenv("FILESHARE_HOLD"); held != "" {
		f, err := os.Open(held)
		if err != nil {
			os.Exit(2)
		}
		os.Stdout.WriteString("held\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		f.Close()
		os.Exit(0)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "held.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReplaceRidesOutAnotherProcessHoldingTheFile$")
	cmd.Env = append(os.Environ(), "FILESHARE_HOLD="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			stdin.Close()
		}
	}
	defer func() {
		release()
		_ = cmd.Wait()
	}()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "held" {
		t.Fatalf("holder said %q, %v, want held", line, err)
	}

	tmp := filepath.Join(dir, "held.txt.tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		err := fileshare.Rename(tmp, path)
		if err == nil || !fileshare.Replacing(err) {
			t.Fatalf("Rename over a held file = %v, want an error Replacing reports", err)
		}
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()
	if err := fileshare.Replace(tmp, path); err != nil {
		t.Fatalf("Replace over a file released within the window = %v, want nil", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("file after Replace = %q, %v, want new", got, err)
	}
}
