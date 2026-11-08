package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// trailerHeads makes one tiny repo whose commits are the heads the cases
// read: no jig.yaml, then one commit for each jig.yaml a head can carry. One
// repo serves every case, since reviewedTrailer reads a revision and nothing
// else.
func trailerHeads(t *testing.T) (dir string, heads map[string]string) {
	t.Helper()
	dir = t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "config", "user.name", "jig-fixture")
	run(t, dir, "config", "user.email", "fixture@example.invalid")
	heads = map[string]string{"none": writeAndCommit(t, dir, "f.txt", "one", "init")}
	for _, h := range []struct{ name, yaml string }{
		{"project", "jig: \"1\"\nproject: demo\nkind: project\npublish:\n  trailer: from the head\n"},
		{"no publish", "jig: \"1\"\nproject: demo\nkind: project\n"},
		{"member", "kind: member\npublish:\n  trailer: from the member\n"},
		{"other project", "jig: \"1\"\nproject: other\nkind: project\npublish:\n  trailer: from the other\n"},
		{"unparsable", "kind: [unclosed\n  : nope\n"},
	} {
		heads[h.name] = writeAndCommit(t, dir, "jig.yaml", h.yaml, "marker: "+h.name)
	}
	// A head whose jig.yaml is in its tree but whose blob git cannot read (the
	// object is gone): the file is there, so this is a failure to read it.
	heads["unreadable"] = writeAndCommit(t, dir, "jig.yaml", "jig: \"1\"\nproject: demo\nkind: project\npublish:\n  trailer: lost with its blob\n", "marker: unreadable")
	blob := run(t, dir, "rev-parse", heads["unreadable"]+":jig.yaml")
	if err := os.Remove(filepath.Join(dir, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatalf("remove the head's jig.yaml blob: %v", err)
	}
	return dir, heads
}

// TestReviewedTrailer: what a render takes as its trailer from each kind of
// reviewed head, for a v3 store serving "demo" and for a v2 store. The head's
// jig.yaml wins when the store takes it as its project's marker, including
// when it sets no trailer (the empty trailer is the head's, not Config's);
// every other head, a rev that does not resolve included, takes Config's
// trailer, and only a head jig.yaml that cannot be read or does not parse, or
// a rev that does not resolve, warns, once.
func TestReviewedTrailer(t *testing.T) {
	t.Parallel()
	dir, heads := trailerHeads(t)
	v3 := project.Config{SchemaVersion: 3, Projects: []string{"demo"}, PublishTrailer: "from the clone"}
	v2 := project.Config{SchemaVersion: 2, PublishTrailer: "from the clone"}
	const clone = "from the clone"
	for _, tc := range []struct {
		name  string
		cfg   project.Config
		rev   string
		want  string
		warns string // a substring of the one warning, "" for none
	}{
		{"the head's wins over Config's", v3, heads["project"], "from the head", ""},
		{"the head's is taken where Config has none", project.Config{SchemaVersion: 3}, heads["project"], "from the head", ""},
		{"no jig.yaml at the head takes Config's", v3, heads["none"], clone, ""},
		{"a head project jig.yaml without publish: has no trailer", v3, heads["no publish"], "", ""},
		{"a member marker takes Config's", v3, heads["member"], clone, ""},
		{"another project's marker takes Config's", v3, heads["other project"], clone, ""},
		{"a v2 store takes Config's", v2, heads["project"], clone, ""},
		{"a v2 store does not read the head's jig.yaml", v2, heads["unparsable"], clone, ""},
		{"an unparsable head jig.yaml takes Config's and warns", v3, heads["unparsable"], clone, "does not parse"},
		{"a jig.yaml whose blob git cannot read takes Config's and warns", v3, heads["unreadable"], clone, "read jig.yaml"},
		{"a rev that does not resolve takes Config's and warns", v3, strings.Repeat("0", 40), clone, "read jig.yaml"},
		{"no rev takes Config's", v3, "", clone, ""},
	} {
		var warns []string
		got := reviewedTrailer(dir, tc.rev, tc.cfg, func(format string, args ...any) {
			warns = append(warns, fmt.Sprintf(format, args...))
		})
		if got != tc.want {
			t.Errorf("%s: trailer = %q, want %q", tc.name, got, tc.want)
		}
		if tc.warns == "" && len(warns) != 0 || tc.warns != "" && (len(warns) != 1 || !strings.Contains(warns[0], tc.warns)) {
			t.Errorf("%s: warnings %q, want %q", tc.name, warns, tc.warns)
		}
	}
}
