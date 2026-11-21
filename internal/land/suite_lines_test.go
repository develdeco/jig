package land_test

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
)

// TestSuitePreconditionVerificationLines: each standing says itself in the
// words of the final run, a project of several repos names the repo, and a
// repo with only an earlier tip run says no suite ran at this head.
func TestSuitePreconditionVerificationLines(t *testing.T) {
	t.Parallel()
	one := func(s land.RepoSuite) string { return land.VerificationLines([]land.RepoSuite{s})[0] }
	cases := []struct {
		name string
		s    land.RepoSuite
		want string
	}{
		{"pass", land.RepoSuite{Repo: "app", Tip: "0123456789abcdef", Standing: land.SuitePass, Command: "go test ./...", Run: journal.Line{Seconds: 12}}, "Suite at the issue's head 0123456789ab: pass in 12 s: `go test ./...`"},
		{"fail", land.RepoSuite{Repo: "app", Tip: "0123456789abcdef", Standing: land.SuiteFail}, "Suite at the issue's head 0123456789ab failed"},
		{"waived", land.RepoSuite{Repo: "app", Tip: "0123456789abcdef", Standing: land.SuiteWaived, Waiver: journal.Line{Summary: "ci runs it"}}, "Suite waived at 0123456789ab: ci runs it"},
		{"undeclared", land.RepoSuite{Repo: "app", Standing: land.SuiteUndeclared}, "No suite declared in app"},
		{"unrun", land.RepoSuite{Repo: "app", Tip: "0123456789abcdef", Standing: land.SuiteUnrun}, "No suite run at the issue's head 0123456789ab"},
	}
	for _, c := range cases {
		if got := one(c.s); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	two := land.VerificationLines([]land.RepoSuite{{Repo: "api", Standing: land.SuiteUndeclared}, {Repo: "web", Standing: land.SuiteUndeclared}})
	if two[0] != "api: No suite declared in api" || two[1] != "web: No suite declared in web" {
		t.Errorf("two repos = %q, want each line naming its repo", two)
	}
}
