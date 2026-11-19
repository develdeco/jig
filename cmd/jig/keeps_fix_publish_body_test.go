package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestKeepsPublishBodyOfSeveralReposNamesEachReposFinalRun: the bodies of a
// ticket that touches several repos print one final_run table with a row for
// each repo, a pass with its seconds and a repo with no run at its reviewed
// head with the line to run.
func TestKeepsPublishBodyOfSeveralReposNamesEachReposFinalRun(t *testing.T) {
	t.Parallel()
	api := verifydeliver.FinalRunReport{Repo: "api", Head: "aaaaaaaaaaaaaaaa", Outcome: verifydeliver.FinalRunPass, Seconds: 12, Command: "go test ./api"}
	web := verifydeliver.FinalRunReport{Repo: "web", Head: "bbbbbbbbbbbbbbbb", Outcome: verifydeliver.FinalRunNone}
	var out strings.Builder
	renderBodies(&out, "store", "JIG-1", verifydeliver.BodyReport{
		Branch:  "jig/JIG-1",
		Covered: []string{"api", "web"},
		Repos: []verifydeliver.RepoBody{
			{Repo: "api", Target: "main", Head: "aaa111", FinalRun: api},
			{Repo: "web", Target: "main", Head: "bbb222", FinalRun: web},
		},
	})
	for _, want := range []string{
		"bodies[2]{repo,base,head,title,pr_body,review_notes}:",
		"final_run[2]{repo,outcome,seconds,command,run}:",
		"  api,pass,12,go test ./api,\n",
		"  web,none at the reviewed head bbbbbbbbbbbb,,,jig verify JIG-1 --final",
	} {
		if !strings.Contains(out.String()+"\n", want) {
			t.Errorf("the body report lacks %q:\n%s", want, out.String())
		}
	}
}
