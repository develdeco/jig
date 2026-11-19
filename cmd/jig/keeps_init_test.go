package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeepsInitFlagsRefuseAndSayWhatTheyPicked: --standalone and --orchestrator
// are two answers to one question, --standalone takes no repos, a malformed
// --key is refused, and each refusal leaves no store behind; an init that
// inferred its mode says what it picked before it writes, and one told by a flag
// does not.
func TestKeepsInitFlagsRefuseAndSayWhatTheyPicked(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	other := cmdGitRepo(t, filepath.Join(parent, "other"))
	e := testEnv(t.TempDir()).inDir(other)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--standalone", "--orchestrator"}, "pick one"},
		{[]string{"--standalone", "somewhere"}, "takes no repos"},
		{[]string{"--key", "lowercase"}, "--key"},
	} {
		var buf bytes.Buffer
		if code := cmdInit(e, tc.args, &buf, strings.NewReader("")); code != 2 || !strings.Contains(buf.String(), tc.want) {
			t.Errorf("jig init %v: exit %d, want 2 saying %q:\n%s", tc.args, code, tc.want, buf.String())
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "other-tickets")); !os.IsNotExist(err) {
		t.Errorf("a refused init left a store behind: %v", err)
	}

	// Inferred: the pick is said, and before what init wrote.
	inferred := cmdGitRepo(t, filepath.Join(parent, "myrepo"))
	var buf bytes.Buffer
	if code := cmdInit(testEnv(t.TempDir()).inDir(inferred), nil, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig init: exit %d\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"picked:\n  mode: standalone\n", "init:\n  mode: standalone\n  project: myrepo\n", "jig share"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig init output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "picked:") > strings.Index(out, "init:") {
		t.Errorf("jig init says what it picked after it wrote:\n%s", out)
	}

	// Told by a flag: nothing was picked.
	flagged := cmdGitRepo(t, filepath.Join(parent, "flagged"))
	buf.Reset()
	if code := cmdInit(testEnv(t.TempDir()).inDir(flagged), []string{"--standalone", "--key", "FL"}, &buf, strings.NewReader("")); code != 0 || strings.Contains(buf.String(), "picked:") {
		t.Errorf("jig init --standalone: exit %d, want 0 and no picked block:\n%s", code, buf.String())
	}
}
