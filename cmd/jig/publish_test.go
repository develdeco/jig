package main

import (
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
