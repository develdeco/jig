package locate

import (
	"runtime"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@github.com/o/r/", "https://GitHub.com/O/R", "git://github.com/o/r", "https://user:tok@github.com:443/o/r.git/"},
		{"/srv/git/r.git", "/srv/git/r", "/srv/git//r.git/"},
		{"file:///srv/git/r.git", "/srv/git/r.git"},
	}
	for _, group := range same {
		want := NormalizeRemote(group[0])
		for _, spelling := range group {
			if got := NormalizeRemote(spelling); got != want {
				t.Errorf("NormalizeRemote(%q) = %q, want %q (as %q)", spelling, got, want, group[0])
			}
		}
	}
	if got := NormalizeRemote("https://github.com/o/r.git"); got != "github.com/o/r" {
		t.Errorf("NormalizeRemote(https://github.com/o/r.git) = %q, want github.com/o/r", got)
	}
	if NormalizeRemote("https://github.com/o/r") == NormalizeRemote("https://github.com/o/other") {
		t.Error("two repos normalize alike")
	}
	if NormalizeRemote("https://github.com/o/r") == NormalizeRemote("https://gitlab.com/o/r") {
		t.Error("two hosts normalize alike")
	}
	if got := NormalizeRemote("  "); got != "" {
		t.Errorf("NormalizeRemote of blanks = %q, want empty", got)
	}
	if runtime.GOOS == "windows" {
		if a, b := NormalizeRemote(`D:\Work\Repo.git`), NormalizeRemote("d:/work/repo"); a != b {
			t.Errorf("Windows path spellings differ: %q vs %q", a, b)
		}
		if a, b := NormalizeRemote("file:///D:/work/repo.git"), NormalizeRemote(`D:\work\repo`); a != b {
			t.Errorf("file URL and Windows path differ: %q vs %q", a, b)
		}
	}
	// A Windows drive letter is a path, not an scp-like host.
	if isRemoteURL(`C:\x\y.git`) || isRemoteURL("C:/x/y.git") {
		t.Error("a drive-letter path was read as a remote URL")
	}
	if !isRemoteURL("git@github.com:o/r.git") || !isRemoteURL("https://h/x") {
		t.Error("a remote URL was read as a path")
	}
}
