package main

import (
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPushedTable: the publish report says, per repo, the head the push left
// on the branch, which is the head the gate reviewed: publish squashes and adds
// nothing, so there is no more to say of it.
func TestPushedTable(t *testing.T) {
	t.Parallel()

	one := verifydeliver.PublishReport{Head: map[string]string{"api": "aaa111"}}
	if got, want := pushedTable(one), "pushed[1]{repo,head}:\n  api,aaa111"; got != want {
		t.Errorf("one branch:\n got %q\nwant %q", got, want)
	}

	two := verifydeliver.PublishReport{Head: map[string]string{"web": "bbb222", "api": "ccc333"}}
	want := "pushed[2]{repo,head}:\n  api,ccc333\n  web,bbb222"
	if got := pushedTable(two); got != want {
		t.Errorf("two branches, by name:\n got %q\nwant %q", got, want)
	}
}

// TestPRURLTable: the publish report says, per repo, the pull request publish
// left and whether it opened it or updated the one the branch already had - the
// two words the confirm question, the journal's pr line and the docs use. A
// repo with no pull-request host still gets a row, naming why in the action
// column (report.PRNote) rather than being dropped with no explanation.
func TestPRURLTable(t *testing.T) {
	t.Parallel()

	report := verifydeliver.PublishReport{
		PRURL: map[string]string{
			"web":   "https://github.example/o/web/pull/2",
			"api":   "https://github.example/o/api/pull/9",
			"local": "",
		},
		PRUpdated: map[string]bool{"api": true, "web": false, "local": false},
		PRNote:    map[string]string{"local": "no pull-request host"},
	}
	want := "pr_url[3]{repo,url,action}:\n  api,\"https://github.example/o/api/pull/9\",updated\n  local,,no pull-request host\n  web,\"https://github.example/o/web/pull/2\",opened"
	if got := prURLTable(report); got != want {
		t.Errorf("a repo updated, a repo opened, and a repo with no host:\n got %q\nwant %q", got, want)
	}

	none := verifydeliver.PublishReport{
		PRURL:  map[string]string{"local": ""},
		PRNote: map[string]string{"local": "no pull-request host"},
	}
	want = "pr_url[1]{repo,url,action}:\n  local,,no pull-request host"
	if got := prURLTable(none); got != want {
		t.Errorf("a repo with no pull-request host:\n got %q\nwant %q", got, want)
	}
}
