package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestVerifyNamesFilesThatHoldRemovedText: a slice's commit removes a long
// sentence from a doc that another tracked file still quotes, and the built
// jig's `jig verify` names that file and line and the doc the sentence left,
// beside a result that is still a covering pass.
func TestVerifyNamesFilesThatHoldRemovedText(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-builder")
	h.Branch(fx.Branch)

	const sentence = "Every retry waits twice as long as the attempt before it did."
	h.Commit("", "setup: a doc and a guide that quotes it", map[string]string{
		"docs/retries.md": "# Retries\n" + sentence + "\n",
		"docs/guide.md":   "Read this first.\nThe rule, as written: " + sentence + "\n",
	})
	h.git("update-ref", "refs/remotes/origin/main", "HEAD")

	h.CommitScenario("a", 1)
	h.Commit("a", "trim the retries doc", map[string]string{"docs/retries.md": "# Retries\n"})

	res := h.Verify(fx.Ticket, "a")
	if res.Code != 0 {
		t.Fatalf("jig verify exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	for _, want := range []string{"state: pass", "covers: yes", "pinned_text[1]{file:line,removed_from}:", `"docs/guide.md:2",docs/retries.md`} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("jig verify output lacks %q\n%s", want, res.Stdout)
		}
	}
}
