package gitx

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/memory"
)

// The tests here are the rules a parser, a table or a pure function decides,
// one row per rule, with no repository built by the git program: a rule a
// row of one of them states is not stated again by a test that lays out a
// repository to reach it.

// TestRuleAtParseHunks: a hunk's new side is "+start" or "+start,count"; a
// "+++" line is a file's new path only inside its header, so a content line
// reading "+++" is not one; a hunk that adds nothing, a deleted file and a
// file with no hunk leave no hunk; a name holding a space or needing quotes
// is read back as git printed it.
func TestRuleAtParseHunks(t *testing.T) {
	cases := []struct {
		name    string
		patch   string
		want    []Hunk
		wantErr bool
	}{
		{"a changed line", "diff --git a/k.txt b/k.txt\nindex 1..2 100644\n--- a/k.txt\n+++ b/k.txt\n@@ -2 +2 @@\n-b\n+B\n",
			[]Hunk{{Path: "k.txt", From: 2, To: 2}}, false},
		{"a hunk of several lines", "diff --git a/k.txt b/k.txt\n--- a/k.txt\n+++ b/k.txt\n@@ -2,0 +3,4 @@\n+a\n+b\n+c\n+d\n",
			[]Hunk{{Path: "k.txt", From: 3, To: 6}}, false},
		{"a content line that reads +++ is no path", "diff --git a/k.txt b/k.txt\n--- a/k.txt\n+++ b/k.txt\n@@ -2 +2 @@\n-b\n+B\n@@ -4 +4 @@\n-d\n+++ plus\n",
			[]Hunk{{Path: "k.txt", From: 2, To: 2}, {Path: "k.txt", From: 4, To: 4}}, false},
		{"a hunk that only deletes", "diff --git a/d.txt b/d.txt\n--- a/d.txt\n+++ b/d.txt\n@@ -2 +1,0 @@\n-2\n", nil, false},
		{"a deleted file", "diff --git a/g.txt b/g.txt\ndeleted file mode 100644\n--- a/g.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n", nil, false},
		{"a new file", "diff --git a/n.txt b/n.txt\nnew file mode 100644\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+o\n",
			[]Hunk{{Path: "n.txt", From: 1, To: 1}}, false},
		{"a binary file has no hunk", "diff --git a/b.bin b/b.bin\nindex 1..2 100644\nBinary files a/b.bin and b/b.bin differ\n", nil, false},
		{"a name with a space", "diff --git a/with space.txt b/with space.txt\n--- a/with space.txt\t\n+++ b/with space.txt\t\n@@ -1 +1,2 @@\n 1\n+2\n",
			[]Hunk{{Path: "with space.txt", From: 1, To: 2}}, false},
		{"a quoted name", "diff --git \"a/q\\tx\" \"b/q\\tx\"\n--- \"a/q\\tx\"\n+++ \"b/q\\tx\"\n@@ -1 +1 @@\n-a\n+b\n",
			[]Hunk{{Path: "q\tx", From: 1, To: 1}}, false},
		{"carriage returns are trimmed", "diff --git a/k.txt b/k.txt\r\n--- a/k.txt\r\n+++ b/k.txt\r\n@@ -1 +1 @@\r\n",
			[]Hunk{{Path: "k.txt", From: 1, To: 1}}, false},
		{"a hunk header it cannot read", "diff --git a/k.txt b/k.txt\n+++ b/k.txt\n@@ nonsense @@\n", nil, true},
		{"a path it cannot unquote", "diff --git a/k.txt b/k.txt\n+++ \"b/bad\\q\"\n@@ -1 +1 @@\n", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseHunks(c.patch)
			if (err != nil) != c.wantErr {
				t.Fatalf("parseHunks error = %v, want error %v", err, c.wantErr)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseHunks = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestRuleAtParseAdded: every "+" line after a hunk header is an added line,
// numbered on from the header's new start; a "+" line inside a file header, in
// a deleted file or in no file adds none.
func TestRuleAtParseAdded(t *testing.T) {
	cases := []struct {
		name    string
		patch   string
		want    []AddedLine
		wantErr bool
	}{
		{"changed lines are numbered from the header", "diff --git a/k.txt b/k.txt\n--- a/k.txt\n+++ b/k.txt\n@@ -2 +2 @@\n-b\n+B\n@@ -4 +4 @@\n-d\n+++ plus\n",
			[]AddedLine{{Path: "k.txt", Line: 2, Text: "B"}, {Path: "k.txt", Line: 4, Text: "++ plus"}}, false},
		{"consecutive added lines", "diff --git a/n.txt b/n.txt\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +5,2 @@\n+x\n+y\n",
			[]AddedLine{{Path: "n.txt", Line: 5, Text: "x"}, {Path: "n.txt", Line: 6, Text: "y"}}, false},
		{"a deleted file adds none", "diff --git a/g.txt b/g.txt\n--- a/g.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n", nil, false},
		{"a file header's +++ line is no added line", "diff --git a/k.txt b/k.txt\n--- a/k.txt\n+++ b/k.txt\n", nil, false},
		{"a binary file adds none", "diff --git a/b.bin b/b.bin\nBinary files a/b.bin and b/b.bin differ\n", nil, false},
		{"a name with a space", "diff --git a/w s.txt b/w s.txt\n--- a/w s.txt\t\n+++ b/w s.txt\t\n@@ -1 +2 @@\n+2\n",
			[]AddedLine{{Path: "w s.txt", Line: 2, Text: "2"}}, false},
		{"a hunk header it cannot read", "diff --git a/k.txt b/k.txt\n+++ b/k.txt\n@@ x @@\n", nil, true},
		{"a path it cannot unquote", "diff --git a/k.txt b/k.txt\n+++ \"b/bad\\q\"\n", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseAdded(c.patch)
			if (err != nil) != c.wantErr {
				t.Fatalf("parseAdded error = %v, want error %v", err, c.wantErr)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseAdded = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestRuleAtLsTreeEntry: an ls-tree entry is "<mode> <type> <object> TAB
// <path>", and anything else is no entry.
func TestRuleAtLsTreeEntry(t *testing.T) {
	cases := []struct {
		entry, typ, path string
		wantErr          bool
	}{
		{"100644 blob 0123456789012345678901234567890123456789\ta.txt", "blob", "a.txt", false},
		{"040000 tree 0123456789012345678901234567890123456789\tsub", "tree", "sub", false},
		{"160000 commit 0123456789012345678901234567890123456789\tmod", "commit", "mod", false},
		{"100644 blob 0123456789012345678901234567890123456789\ta b\tc", "blob", "a b\tc", false},
		{"100644 blob 0123456789012345678901234567890123456789 a.txt", "", "", true},
		{"100644 blob\ta.txt", "", "", true},
	}
	for _, c := range cases {
		typ, path, err := lsTreeEntry(c.entry)
		if (err != nil) != c.wantErr || typ != c.typ || path != c.path {
			t.Errorf("lsTreeEntry(%q) = %q, %q, %v; want %q, %q, error %v", c.entry, typ, path, err, c.typ, c.path, c.wantErr)
		}
	}
}

// TestRuleAtRefFileContent: a loose ref file is a symbolic ref naming a ref
// under refs/, or an object name followed by the end or white space; the
// vertical tab is not white space to git, and capitals are an object name
// kept in lower case.
func TestRuleAtRefFileContent(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	for _, c := range []struct {
		name, content string
		want          refState
		oid, target   string
	}{
		{"an object name", sha + "\n", refFound, sha, ""},
		{"an object name with trailing text after white space", sha + " trailing\n", refFound, sha, ""},
		{"an object name with no newline", sha, refFound, sha, ""},
		{"an object name in capitals", strings.ToUpper("abcdef0123456789abcdef0123456789abcdef01") + "\n", refFound, "abcdef0123456789abcdef0123456789abcdef01", ""},
		{"an object name run into other text", sha + "x\n", refUnsure, "", ""},
		{"a short object name", sha[:39] + "\n", refUnsure, "", ""},
		{"an empty file", "", refUnsure, "", ""},
		{"a symbolic ref", "ref: refs/heads/nothere\n", refFound, "", "refs/heads/nothere"},
		{"a symbolic ref with no space after the colon", "ref:refs/heads/a\n", refFound, "", "refs/heads/a"},
		{"a symbolic ref with a bad name", "ref: nothere\n", refUnsure, "", ""},
		{"a symbolic ref to a name with a space", "ref: refs/heads/a b\n", refUnsure, "", ""},
		{"a vertical tab is not white space to git", sha + "\v\n", refUnsure, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, state := parseRefContent(c.content)
			if state != c.want || got.oid != c.oid || got.target != c.target {
				t.Errorf("parseRefContent(%q) = %+v, %v; want oid %q target %q state %v", c.content, got, state, c.oid, c.target, c.want)
			}
		})
	}
}

// TestRuleAtRefAndHeadNames: the names a HEAD or a symbolic ref may hold, and
// what makes a name one git's check-ref-format would turn away.
func TestRuleAtRefAndHeadNames(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	names := []struct {
		name string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/a/b", true},
		{"refs/tags/v1.0", true},
		{"heads/main", false},
		{"refs/", false},
		{"refs/heads/", false},
		{"refs/heads/a.", false},
		{"refs/heads/a..b", false},
		{"refs/heads//a", false},
		{"refs/heads/a@{1}", false},
		{"refs/heads/a b", false},
		{"refs/heads/a~", false},
		{"refs/heads/a^", false},
		{"refs/heads/a:b", false},
		{"refs/heads/a?", false},
		{"refs/heads/a*", false},
		{"refs/heads/a[", false},
		{"refs/heads/a\\b", false},
		{"refs/heads/a\x01", false},
		{"refs/heads/a\x7f", false},
		{"refs/heads/.hidden", false},
		{"refs/heads/main.lock", false},
		{"refs/heads/a.lock/b", false},
	}
	for _, c := range names {
		if got := plausibleRefName(c.name); got != c.want {
			t.Errorf("plausibleRefName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	heads := []struct {
		head string
		want bool
	}{
		{"ref: refs/heads/main", true},
		{"ref:\trefs/heads/main", true},
		{sha, true},
		{strings.ToUpper(sha), true},
		{"garbage", false},
		{"ref: heads/main", false},
		{"ref: refs/heads/a b", false},
		{sha[:39], false},
		{sha + "0", false},
		{"", false},
	}
	for _, c := range heads {
		if got := validHead(c.head); got != c.want {
			t.Errorf("validHead(%q) = %v, want %v", c.head, got, c.want)
		}
	}
	for s, want := range map[string]bool{sha: true, sha[:39]: false, sha + "0": false, strings.Repeat("g", 40): false, strings.Repeat("F", 40): true} {
		if got := isHex40(s); got != want {
			t.Errorf("isHex40(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestRuleAtConfigDeclines: a repository's config is read in process only
// when it holds nothing the reads would not honor: a bare repository, a work
// tree of its own, an extension (even in format 0), a format above 1, an
// include of either kind, or a file that does not parse is the git program's.
func TestRuleAtConfigDeclines(t *testing.T) {
	cases := []struct {
		name, config string
		missing      bool
		want         bool
	}{
		{name: "a plain config", config: plainConfig},
		{name: "no config", missing: true},
		{name: "core.bare false", config: plainConfig},
		{name: "repository format 1", config: "[core]\n\trepositoryformatversion = 1\n"},
		{name: "core.bare true", config: "[core]\n\trepositoryformatversion = 0\n\tbare = true\n", want: true},
		{name: "core.bare with no value", config: "[core]\n\trepositoryformatversion = 0\n\tbare\n", want: true},
		{name: "core.worktree", config: plainConfig + "\tworktree = ../elsewhere\n", want: true},
		{name: "an extension", config: "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tobjectformat = sha256\n", want: true},
		{name: "an extension in version 0", config: plainConfig + "[extensions]\n\tworktreeConfig = true\n", want: true},
		{name: "repository format 2", config: "[core]\n\trepositoryformatversion = 2\n", want: true},
		{name: "an include", config: plainConfig + "[include]\n\tpath = other\n", want: true},
		{name: "a conditional include", config: plainConfig + "[includeIf \"gitdir:/x/\"]\n\tpath = other\n", want: true},
		{name: "a config that does not parse", config: "[core\n\tbare = false\n", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if !c.missing {
				writeFile(t, filepath.Join(dir, "config"), c.config)
			}
			if got := configDeclines(dir); got != c.want {
				t.Errorf("configDeclines = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("a config that is a directory", func(t *testing.T) {
		dir := t.TempDir()
		mkdir(t, filepath.Join(dir, "config"))
		if !configDeclines(dir) {
			t.Errorf("configDeclines answered for a config it cannot read")
		}
	})
}

// TestRuleAtDeclinedEnv: each variable that moves where git looks or what it
// finds leaves discovery to the git program, even set to nothing, and a
// repository with none of them set is answered.
func TestRuleAtDeclinedEnv(t *testing.T) {
	work, _ := fakeRepo(t)
	if _, ok := discover(work, envOf(nil)); !ok {
		t.Fatal("the plain repository is declined")
	}
	for _, name := range []string{"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_NAMESPACE", "GIT_REPLACE_REF_BASE", "GIT_GRAFT_FILE", "GIT_SHALLOW_FILE"} {
		for _, value := range []string{"", "x"} {
			if _, ok := discover(work, envOf(map[string]string{name: value})); ok {
				t.Errorf("discover answered with %s=%q set", name, value)
			}
		}
	}
	if _, ok := discover("", envOf(nil)); ok {
		t.Errorf("discover answered for no directory")
	}
}

// writeIndexData lays out an index as the git program writes it: version 2
// or 3 pads each entry to a multiple of 8 bytes, version 4 prefix-compresses
// the path behind a varint; flags[i] is entry i's flag word, and a flag word
// with the extended bit carries two more bytes.
func writeIndexData(version uint32, paths []string, flags []uint16) []byte {
	head := make([]byte, 12)
	copy(head, "DIRC")
	binary.BigEndian.PutUint32(head[4:], version)
	binary.BigEndian.PutUint32(head[8:], uint32(len(paths)))
	data := head
	for i, p := range paths {
		entry := make([]byte, 62)
		binary.BigEndian.PutUint16(entry[60:], flags[i])
		if flags[i]&0x4000 != 0 {
			entry = append(entry, 0, 0)
		}
		if version == 4 {
			entry = append(entry, 0) // nothing stripped from the previous path
			entry = append(entry, p...)
			entry = append(entry, 0)
		} else {
			entry = append(entry, p...)
			entry = append(entry, 0)
			for len(entry)%8 != 0 {
				entry = append(entry, 0)
			}
		}
		data = append(data, entry...)
	}
	return append(data, make([]byte, 20)...) // the checksum, which is not read
}

// withCount is data with its header's entry count changed.
func withCount(data []byte, n uint32) []byte {
	binary.BigEndian.PutUint32(data[8:], n)
	return data
}

// TestRuleAtAssumeUnchangedInTheIndex: an index entry carrying the
// assume-valid flag, in any version and behind any other entry, is found; a
// plain index, an empty one, and an index too short to walk are not trusted.
func TestRuleAtAssumeUnchangedInTheIndex(t *testing.T) {
	const assume, extended = 0x8000, 0x4000
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"a plain version 2 index", writeIndexData(2, []string{"a", "bb"}, []uint16{1, 2}), false},
		{"an empty index", writeIndexData(2, nil, nil), false},
		{"assume-valid in version 2", writeIndexData(2, []string{"a", "bb"}, []uint16{1, 2 | assume}), true},
		{"assume-valid on the first entry", writeIndexData(2, []string{"a", "bb"}, []uint16{1 | assume, 2}), true},
		{"a plain version 3 index with an extended flag", writeIndexData(3, []string{"a", "bb"}, []uint16{1 | extended, 2}), false},
		{"assume-valid behind an extended entry", writeIndexData(3, []string{"a", "bb"}, []uint16{1 | extended, 2 | assume}), true},
		{"a plain version 4 index", writeIndexData(4, []string{"a", "bb"}, []uint16{1, 2}), false},
		{"assume-valid in version 4", writeIndexData(4, []string{"a", "bb"}, []uint16{1, 2 | assume}), true},
		{"no signature", []byte("not an index at all"), true},
		{"a header only", []byte("DIRC\x00\x00\x00\x02"), true},
		{"fewer entries than the header counts", withCount(writeIndexData(2, []string{"a"}, []uint16{1}), 3), true},
		{"an entry cut inside its stat data", writeIndexData(2, []string{"a", "bb"}, []uint16{1, 2})[:12+30], true},
		{"a path with no end", withCount(append(append(writeIndexData(2, nil, nil)[:12], make([]byte, 62)...), 'x'), 1), true},
	}
	for _, c := range cases {
		if got := assumesUnchanged(c.data); got != c.want {
			t.Errorf("%s: assumesUnchanged = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRuleAtIndexEncoding: an entry go-git cannot write, a time before 1970,
// leaves the commit to the git program; one it can write is encoded, sorted.
func TestRuleAtIndexEncoding(t *testing.T) {
	entry := func(name string, when time.Time) *index.Entry {
		return &index.Entry{Name: name, Mode: filemode.Regular, ModifiedAt: when, CreatedAt: when, Hash: plumbing.NewHash("0123456789012345678901234567890123456789")}
	}
	if _, err := EncodeIndex(&index.Index{Version: 2, Entries: []*index.Entry{entry("old.txt", time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC))}}); err == nil {
		t.Errorf("an entry dated before 1970 was encoded")
	}
	idx := &index.Index{Version: 2, Entries: []*index.Entry{entry("b", time.Unix(1700000000, 0)), entry("a", time.Unix(1700000000, 0))}}
	data, err := EncodeIndex(idx)
	if err != nil {
		t.Fatalf("EncodeIndex: %v", err)
	}
	if idx.Entries[0].Name != "a" {
		t.Errorf("entries not sorted: %s first", idx.Entries[0].Name)
	}
	if assumesUnchanged(data) {
		t.Errorf("an index gitx encoded reads as assume-unchanged")
	}
}

// TestRuleAtIgnoreRuleFiles: gitx matches ignore rules with go-git's matcher
// only where that agrees with the git program: plain globs are read, behind a
// byte-order mark and between comments and blank lines; a negation, a "**",
// a bracket expression, a backslash escape, trailing white space or a
// character beyond ASCII returns ErrUseCLI; under core.ignoreCase the rules
// match case-insensitively; no file has no rules.
func TestRuleAtIgnoreRuleFiles(t *testing.T) {
	cases := []struct {
		name, content string
		ignoreCase    bool
		patterns      int
		want          error
	}{
		{name: "plain globs", content: "*.lock\nvendor/\n", patterns: 2},
		{name: "a byte-order mark on the first line", content: "\uFEFF*.lock\n", patterns: 1},
		{name: "a byte-order mark past the first line is not skipped", content: "*.lock\n\uFEFFa\n", want: ErrUseCLI},
		{name: "comments and blank lines", content: "# note\n\n   \n*.tmp\n", patterns: 1},
		{name: "a comment beyond ASCII", content: "# caf\u00e9\n*.tmp\n", patterns: 1},
		{name: "a negation", content: "*.lock\n!keep.lock\n", want: ErrUseCLI},
		{name: "a double star", content: "cache/**\n", want: ErrUseCLI},
		{name: "a bracket expression", content: "[Jj]ournal\n", want: ErrUseCLI},
		{name: "an escape", content: "\\#hash\n", want: ErrUseCLI},
		{name: "trailing space", content: "*.tmp \n", want: ErrUseCLI},
		{name: "trailing tab", content: "*.tmp\t\n", want: ErrUseCLI},
		{name: "a rule beyond ASCII", content: "caf\u00e9*\n", want: ErrUseCLI},
		{name: "plain globs under ignoreCase", content: "*.LOCK\n", ignoreCase: true, patterns: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ignore")
			writeFile(t, path, c.content)
			s := &scan{ignoreCase: c.ignoreCase}
			ps, err := s.readPatterns(path, nil)
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("readPatterns = %v, want %v", err, c.want)
			}
			if err == nil && len(ps) != c.patterns {
				t.Fatalf("readPatterns read %d rules, want %d", len(ps), c.patterns)
			}
			if c.ignoreCase && err == nil {
				if !gitignore.NewMatcher(ps).Match(s.key([]string{"Journal.LOCK"}), false) {
					t.Errorf("*.LOCK does not match Journal.LOCK under ignoreCase")
				}
			}
		})
	}
	t.Run("no file", func(t *testing.T) {
		s := &scan{}
		ps, err := s.readPatterns(filepath.Join(t.TempDir(), "missing"), nil)
		if err != nil || ps != nil {
			t.Errorf("readPatterns of no file = %v, %v; want none", ps, err)
		}
	})
	t.Run("a directory in place of the file", func(t *testing.T) {
		s := &scan{}
		dir := t.TempDir()
		if _, err := s.readPatterns(dir, nil); err == nil {
			t.Errorf("readPatterns of a directory succeeded")
		}
	})
	for s, want := range map[string]bool{"": true, "plain": true, "caf\u00e9": false, "a\x80": false} {
		if got := isASCII(s); got != want {
			t.Errorf("isASCII(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestRuleAtScanSettings: the repository's config sets what a scan honors:
// the executable bit counts unless core.fileMode is false, and
// core.ignoreCase and core.precomposeUnicode are off unless true.
func TestRuleAtScanSettings(t *testing.T) {
	cases := []struct {
		name                          string
		options                       map[string]string
		fileMode, ignoreCase, precomp bool
	}{
		{"nothing set", nil, true, false, false},
		{"core.fileMode false", map[string]string{"fileMode": "false"}, false, false, false},
		{"core.fileMode true", map[string]string{"fileMode": "true"}, true, false, false},
		{"core.ignoreCase true", map[string]string{"ignoreCase": "true"}, true, true, false},
		{"core.precomposeUnicode true", map[string]string{"precomposeUnicode": "yes"}, true, false, true},
		{"core.ignoreCase false", map[string]string{"ignoreCase": "false"}, true, false, false},
	}
	for _, c := range cases {
		cfg := config.NewConfig()
		for k, v := range c.options {
			cfg.Raw.Section("core").SetOption(k, v)
		}
		s, err := newScan(nil, "", cfg, &index.Index{}, time.Time{})
		if err != nil {
			t.Fatalf("%s: newScan: %v", c.name, err)
		}
		if s.fileMode != c.fileMode || s.ignoreCase != c.ignoreCase || s.precompose != c.precomp {
			t.Errorf("%s: fileMode %v ignoreCase %v precompose %v; want %v %v %v", c.name, s.fileMode, s.ignoreCase, s.precompose, c.fileMode, c.ignoreCase, c.precomp)
		}
	}
}

// TestRuleAtIndexEntriesAScanTrusts: an index entry that is not a plain
// tracked file, or two tracked names core.ignoreCase makes one, is the git
// program's.
func TestRuleAtIndexEntriesAScanTrusts(t *testing.T) {
	entry := func(name string, mode filemode.FileMode) *index.Entry { return &index.Entry{Name: name, Mode: mode} }
	cases := []struct {
		name       string
		entries    []*index.Entry
		ignoreCase bool
		want       error
	}{
		{"plain files", []*index.Entry{entry("a", filemode.Regular), entry("b", filemode.Regular)}, false, nil},
		{"an executable", []*index.Entry{entry("run.sh", filemode.Executable)}, false, ErrUseCLI},
		{"a symbolic link", []*index.Entry{entry("link", filemode.Symlink)}, false, ErrUseCLI},
		{"a submodule", []*index.Entry{entry("mod", filemode.Submodule)}, false, ErrUseCLI},
		{"a conflict stage", []*index.Entry{{Name: "c", Mode: filemode.Regular, Stage: index.AncestorMode}}, false, ErrUseCLI},
		{"a sparse entry", []*index.Entry{{Name: "s", Mode: filemode.Regular, SkipWorktree: true}}, false, ErrUseCLI},
		{"an intent-to-add entry", []*index.Entry{{Name: "i", Mode: filemode.Regular, IntentToAdd: true}}, false, ErrUseCLI},
		{"two names that differ in case", []*index.Entry{entry("NOTES.txt", filemode.Regular), entry("notes.txt", filemode.Regular)}, false, nil},
		{"two names core.ignoreCase makes one", []*index.Entry{entry("NOTES.txt", filemode.Regular), entry("notes.txt", filemode.Regular)}, true, ErrUseCLI},
	}
	for _, c := range cases {
		cfg := config.NewConfig()
		if c.ignoreCase {
			cfg.Raw.Section("core").SetOption("ignoreCase", "true")
		}
		_, err := newScan(nil, "", cfg, &index.Index{Entries: c.entries}, time.Time{})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: newScan = %v, want %v", c.name, err, c.want)
		}
	}
}

// TestRuleAtScanWalk: what a walk of the work tree leaves to the git program
// (a symbolic link, an executable where core.fileMode counts it, an embedded
// repository, a .gitattributes below the root, a name beyond ASCII where it
// would be compared or matched differently, an untracked name core.ignoreCase
// would match to a tracked one) and what it adds as a plain file, whatever
// the executable bit says where core.fileMode is off.
func TestRuleAtScanWalk(t *testing.T) {
	type setup struct {
		fileMode, ignoreCase, precompose bool
		tracked                          []string
		rules                            []string
		lay                              func(t *testing.T, dir string)
	}
	file := func(rel string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), "x\n") }
	}
	exec := func(t *testing.T, dir string) {
		path := filepath.Join(dir, "run.sh")
		writeFile(t, path, "#!/bin/sh\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(path); err != nil || fi.Mode()&0o111 == 0 {
			t.Skip("no executable bit here")
		}
	}
	cases := []struct {
		name  string
		s     setup
		want  error
		added []string
	}{
		{"a plain file", setup{fileMode: true, lay: file("a.txt")}, nil, []string{"a.txt"}},
		{"a symbolic link", setup{fileMode: true, lay: func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "target"), "x\n")
			if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
				t.Skipf("no symbolic links here: %v", err)
			}
		}}, ErrUseCLI, nil},
		{"an executable where core.fileMode counts it", setup{fileMode: true, lay: exec}, ErrUseCLI, nil},
		{"an executable where core.fileMode is off", setup{lay: exec}, nil, []string{"run.sh"}},
		{"an embedded repository", setup{fileMode: true, lay: func(t *testing.T, dir string) {
			mkdir(t, filepath.Join(dir, "inner", ".git"))
		}}, ErrUseCLI, nil},
		{"the root .gitattributes", setup{fileMode: true, lay: file(".gitattributes")}, nil, []string{".gitattributes"}},
		{"a .gitattributes below the root", setup{fileMode: true, lay: file("ticket-a/.gitattributes")}, ErrUseCLI, nil},
		{"a name core.precomposeUnicode would recompose", setup{fileMode: true, precompose: true, lay: file("caf\u00e9.txt")}, ErrUseCLI, nil},
		{"an ASCII name under core.precomposeUnicode", setup{fileMode: true, precompose: true, lay: file("cafe.txt")}, nil, []string{"cafe.txt"}},
		{"a name beyond ASCII with no ignore rules", setup{fileMode: true, lay: file("caf\u00e9.txt")}, nil, []string{"caf\u00e9.txt"}},
		{"a name beyond ASCII under ignore rules", setup{fileMode: true, rules: []string{"*.tmp"}, lay: file("caf\u00e9.txt")}, ErrUseCLI, nil},
		{"an untracked file the ignore rules exclude", setup{fileMode: true, rules: []string{"*.lock"}, lay: file("journal.lock")}, nil, nil},
		{"an untracked name core.ignoreCase matches to a tracked one", setup{fileMode: true, ignoreCase: true, tracked: []string{"notes.txt"}, lay: file("NOTES.txt")}, ErrUseCLI, nil},
		{"the same name where core.ignoreCase is off", setup{fileMode: true, tracked: []string{"notes.txt"}, lay: file("NOTES.txt")}, nil, []string{"NOTES.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, err := git.Init(memory.NewStorage(), nil)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			c.s.lay(t, dir)
			s := &scan{
				repo: repo, dir: dir, idx: &index.Index{},
				fileMode: c.s.fileMode, ignoreCase: c.s.ignoreCase, precompose: c.s.precompose,
				tracked: map[string]*index.Entry{}, folded: map[string]bool{}, seen: map[string]bool{},
			}
			for _, name := range c.s.tracked {
				s.folded[strings.ToLower(name)] = true
			}
			var ps []gitignore.Pattern
			for _, r := range c.s.rules {
				ps = append(ps, gitignore.ParsePattern(r, nil))
			}
			err = s.walk(nil, dir, ps, false)
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("walk = %v, want %v", err, c.want)
			}
			if err != nil {
				return
			}
			var got []string
			for _, e := range s.idx.Entries {
				got = append(got, e.Name)
				if e.Mode != filemode.Regular {
					t.Errorf("%s recorded as mode %v, want a plain file", e.Name, e.Mode)
				}
			}
			if !reflect.DeepEqual(got, c.added) {
				t.Errorf("walk added %v, want %v", got, c.added)
			}
		})
	}
}

// TestRuleAtSignatureEnvironment: GIT_<role>_NAME, _EMAIL and _DATE win over
// the identity passed in and over now, role by role, and an empty one is
// unset; a pinned date gitx cannot read is the git program's.
func TestRuleAtSignatureEnvironment(t *testing.T) {
	id := Identity{Name: "tester", Email: "tester@example.invalid"}
	when := time.Unix(1700000000, 0)
	for _, c := range []struct {
		name         string
		env          map[string]string
		role         string
		wantName     string
		wantEmail    string
		wantDate     string
		wantDateFail bool
	}{
		{"nothing set", nil, "AUTHOR", "tester", "tester@example.invalid", "", false},
		{"the author's own", map[string]string{"GIT_AUTHOR_NAME": "a", "GIT_AUTHOR_EMAIL": "a@x.invalid", "GIT_AUTHOR_DATE": "2026-01-01T00:00:00Z"}, "AUTHOR", "a", "a@x.invalid", "2026-01-01T00:00:00Z", false},
		{"the author's do not reach the committer", map[string]string{"GIT_AUTHOR_NAME": "a", "GIT_AUTHOR_DATE": "2026-01-01T00:00:00Z"}, "COMMITTER", "tester", "tester@example.invalid", "", false},
		{"the committer's own", map[string]string{"GIT_COMMITTER_NAME": "c", "GIT_COMMITTER_DATE": "1767225600 +0100"}, "COMMITTER", "c", "tester@example.invalid", "2026-01-01T01:00:00+01:00", false},
		{"empty values are unset", map[string]string{"GIT_AUTHOR_NAME": "", "GIT_AUTHOR_EMAIL": "", "GIT_AUTHOR_DATE": ""}, "AUTHOR", "tester", "tester@example.invalid", "", false},
		{"a date it cannot read", map[string]string{"GIT_AUTHOR_DATE": "yesterday noon"}, "AUTHOR", "tester", "tester@example.invalid", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := envOf(c.env)
			at, err := signatureTime(c.role, env)
			if c.wantDateFail {
				if !errors.Is(err, ErrUseCLI) {
					t.Fatalf("signatureTime = %v, want ErrUseCLI", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("signatureTime: %v", err)
			}
			if c.wantDate != "" {
				if got := at.Format(time.RFC3339); got != c.wantDate {
					t.Errorf("signatureTime = %s, want %s", got, c.wantDate)
				}
			} else if d := time.Since(at); d < 0 || d > time.Minute {
				t.Errorf("signatureTime with no date pinned = %v, want now", at)
			}
			sig := signature(c.role, id, when, env)
			if sig.Name != c.wantName || sig.Email != c.wantEmail || !sig.When.Equal(when) {
				t.Errorf("signature = %q <%s> %v; want %q <%s> %v", sig.Name, sig.Email, sig.When, c.wantName, c.wantEmail, when)
			}
		})
	}
}

// remoteConfigFixture lays out a work repository and a bare origin the way
// a fetch or push finds them, with no process: the work's config and the
// origin's are written as the git program writes them, so a row adds only
// the line it is about.
type remoteConfigFixture struct {
	url        string   // the remote's url; "" for the origin's own path
	extraURLs  []string // more url lines
	fetch      string   // the fetch refspec; "" for the default
	remoteOpts string   // lines inside [remote "origin"]
	workExtra  string   // sections after the work's own
	originConf string   // sections added to the origin's config
	originFile string   // a file laid in the origin, relative; "" for none
	nonBare    bool     // the origin is a work tree's repository
	dotGit     bool     // the url names its .git directory
}

// TestRuleAtLocalRemote: only a bare repository at a local path, with a
// config and a layout the in-process copy honors, is served in process; a
// network url, a second url, a push url, its own receive-pack or upload-pack,
// mirroring, URL rewriting, a refspec other than the default, objects checked
// on transfer, a hook on the origin, and a non-bare origin (named by its
// work tree or by its .git) are the git program's.
func TestRuleAtLocalRemote(t *testing.T) {
	cases := []struct {
		name string
		f    remoteConfigFixture
		want error
	}{
		{"a bare repository at a local path", remoteConfigFixture{}, nil},
		{"a network url", remoteConfigFixture{url: "https://example.invalid/store.git"}, ErrUseCLI},
		{"an scp-style url", remoteConfigFixture{url: "git@example.invalid:store.git"}, ErrUseCLI},
		{"a second url", remoteConfigFixture{extraURLs: []string{"https://example.invalid/b.git"}}, ErrUseCLI},
		{"a push URL", remoteConfigFixture{remoteOpts: "pushurl = somewhere\n"}, ErrUseCLI},
		{"its own receive-pack", remoteConfigFixture{remoteOpts: "receivepack = git-receive-pack\n"}, ErrUseCLI},
		{"its own upload-pack", remoteConfigFixture{remoteOpts: "uploadpack = git-upload-pack\n"}, ErrUseCLI},
		{"mirroring", remoteConfigFixture{remoteOpts: "mirror = true\n"}, ErrUseCLI},
		{"another vcs", remoteConfigFixture{remoteOpts: "vcs = hg\n"}, ErrUseCLI},
		{"URL rewriting", remoteConfigFixture{workExtra: "[url \"elsewhere\"]\n\tinsteadOf = store:\n"}, ErrUseCLI},
		{"another fetch refspec", remoteConfigFixture{fetch: "+refs/heads/main:refs/remotes/origin/main"}, ErrUseCLI},
		{"objects checked on fetch", remoteConfigFixture{workExtra: "[fetch]\n\tfsckObjects = true\n"}, ErrUseCLI},
		{"objects checked on receive, on the origin", remoteConfigFixture{originConf: "[receive]\n\tfsckObjects = true\n"}, ErrUseCLI},
		{"objects checked on every transfer, as a bare key", remoteConfigFixture{originConf: "[transfer]\n\tfsckObjects\n"}, ErrUseCLI},
		{"objects checking turned off", remoteConfigFixture{originConf: "[transfer]\n\tfsckObjects = false\n"}, nil},
		{"the origin's own hook", remoteConfigFixture{originFile: "hooks/post-receive"}, ErrUseCLI},
		{"a hook sample", remoteConfigFixture{originFile: "hooks/post-receive.sample"}, nil},
		{"a work tree as the origin", remoteConfigFixture{nonBare: true}, ErrUseCLI},
		{"a work tree's .git as the origin", remoteConfigFixture{nonBare: true, dotGit: true}, ErrUseCLI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			origin := filepath.Join(root, "origin.git")
			if c.f.nonBare {
				origin = filepath.Join(root, "origin")
			}
			if _, err := git.PlainInit(origin, !c.f.nonBare); err != nil {
				t.Fatal(err)
			}
			if c.f.originConf != "" {
				path := filepath.Join(origin, "config")
				if c.f.nonBare {
					path = filepath.Join(origin, ".git", "config")
				}
				prev, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(prev)+c.f.originConf)
			}
			if c.f.originFile != "" {
				writeFile(t, filepath.Join(origin, filepath.FromSlash(c.f.originFile)), "#!/bin/sh\n")
			}
			url := c.f.url
			if url == "" {
				url = filepath.ToSlash(origin)
				if c.f.dotGit {
					url += "/.git"
				}
			}
			fetch := c.f.fetch
			if fetch == "" {
				fetch = "+refs/heads/*:refs/remotes/origin/*"
			}
			work := filepath.Join(root, "work")
			if _, err := git.PlainInit(work, false); err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			b.WriteString("[core]\n\trepositoryformatversion = 0\n\tbare = false\n[remote \"origin\"]\n\turl = " + url + "\n")
			for _, u := range c.f.extraURLs {
				b.WriteString("\turl = " + u + "\n")
			}
			b.WriteString("\tfetch = " + fetch + "\n" + indentOptions(c.f.remoteOpts) + c.f.workExtra)
			writeFile(t, filepath.Join(work, ".git", "config"), b.String())
			repo, err := git.PlainOpen(work)
			if err != nil {
				t.Fatal(err)
			}
			rem, err := localRemote(repo, work, "origin")
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("localRemote = %v, want %v", err, c.want)
			}
			if rem != nil {
				rem.done()
			}
		})
	}
	t.Run("a remote that is not configured", func(t *testing.T) {
		work := t.TempDir()
		repo, err := git.PlainInit(work, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := localRemote(repo, work, "origin"); err == nil || errors.Is(err, ErrUseCLI) {
			t.Errorf("localRemote of an unknown remote = %v, want a plain error", err)
		}
	})
}

// indentOptions puts the tab git writes before each option line.
func indentOptions(lines string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(lines, "\n"), "\n") {
		if l != "" {
			b.WriteString("\t" + l + "\n")
		}
	}
	return b.String()
}

// TestRuleAtAutoGCOnReceive: a remote whose receive.autoGC says off, in any
// of git's spellings of false, is not started on maintenance after a push.
func TestRuleAtAutoGCOnReceive(t *testing.T) {
	for _, v := range []string{"false", "FALSE", "no", "off", "0"} {
		dir := t.TempDir()
		repo, err := git.PlainInit(dir, true)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := repo.Config()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Raw.Section("receive").SetOption("autoGC", v)
		if err := repo.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if autoGCOnReceive(repo, dir) {
			t.Errorf("autoGCOnReceive with receive.autoGC = %s started maintenance", v)
		}
	}
}
