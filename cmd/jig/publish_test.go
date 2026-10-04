package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPushedTable: the publish report says, per repo, the head the push left
// on the branch and whether publish squashed it - or pushed it as it was
// because it was already on origin, in the words the report is documented to
// use (verifydeliver.NotSquashed).
func TestPushedTable(t *testing.T) {
	t.Parallel()

	squashed := verifydeliver.PublishReport{
		Squashed: map[string]string{"api": "aaa111"},
		Head:     map[string]string{"api": "aaa111"},
	}
	if got, want := pushedTable(squashed), "pushed[1]{repo,head,squash}:\n  api,aaa111,squashed"; got != want {
		t.Errorf("a squashed branch:\n got %q\nwant %q", got, want)
	}

	asIs := verifydeliver.PublishReport{
		Squashed: map[string]string{},
		Head:     map[string]string{"web": "bbb222", "api": "ccc333"},
	}
	want := "pushed[2]{repo,head,squash}:\n  api,ccc333,not squashed (branch already on origin)\n  web,bbb222,not squashed (branch already on origin)"
	if got := pushedTable(asIs); got != want {
		t.Errorf("branches already on origin:\n got %q\nwant %q", got, want)
	}
}

// TestPRURLTable: the publish report says, per repo, the pull request publish
// left and whether it opened it or updated the one the branch already had - the
// two words the confirm question, the journal's pr line and the docs use.
// A repo whose tracker opens no pull requests has no row.
func TestPRURLTable(t *testing.T) {
	t.Parallel()

	report := verifydeliver.PublishReport{
		PRURL: map[string]string{
			"web":   "https://github.example/o/web/pull/2",
			"api":   "https://github.example/o/api/pull/9",
			"local": "",
		},
		PRUpdated: map[string]bool{"api": true, "web": false, "local": false},
	}
	want := "pr_url[2]{repo,url,action}:\n  api,\"https://github.example/o/api/pull/9\",updated\n  web,\"https://github.example/o/web/pull/2\",opened"
	if got := prURLTable(report); got != want {
		t.Errorf("a repo updated and a repo opened:\n got %q\nwant %q", got, want)
	}

	none := verifydeliver.PublishReport{PRURL: map[string]string{"local": ""}, PRUpdated: map[string]bool{"local": false}}
	if got, want := prURLTable(none), "pr_url[0]"; !strings.HasPrefix(got, want) {
		t.Errorf("a tracker that opens no pull request: %q, want a table with no rows (%s...)", got, want)
	}
}
