package main

import (
	"reflect"
	"strings"
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

// TestRenderBodies: what `jig publish --body` prints. The body of the one repo of
// a ticket is a block of its facts, as it always was; a ticket in one repo of a
// project of several is told to record its pull request with that repo; the
// bodies of several are a table with a row for each repo.
func TestRenderBodies(t *testing.T) {
	t.Parallel()
	root := "store"
	api := verifydeliver.RepoBody{Repo: "api", Target: "main", Head: "aaa111", Title: "JIG-1: total", PRBody: "tickets/JIG-1/pr/api.md", ReviewNotes: "tickets/JIG-1/pr/review-notes.md"}
	web := verifydeliver.RepoBody{Repo: "web", Target: "develop", Head: "bbb222", Title: "JIG-1: total", PRBody: "tickets/JIG-1/pr/web.md", ReviewNotes: "tickets/JIG-1/pr/review-notes-web.md"}

	var out strings.Builder
	renderBodies(&out, root, "JIG-1", verifydeliver.BodyReport{Branch: "jig/JIG-1", Covered: []string{"api"}, Repos: []verifydeliver.RepoBody{api}})
	for _, want := range []string{"publish_body:", "repo: api", "branch: jig/JIG-1", "base: main", "head: aaa111", "api.md", "`jig publish JIG-1 --pr <url>`", "`jig publish JIG-1`"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the body of one repo lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "--repo") {
		t.Errorf("the body of a ticket in one repo mentions --repo:\n%s", out.String())
	}

	out.Reset()
	renderBodies(&out, root, "JIG-1", verifydeliver.BodyReport{Branch: "jig/JIG-1", Covered: []string{"api", "web"}, Repos: []verifydeliver.RepoBody{web}})
	for _, want := range []string{"repo: web", "base: develop", "`jig publish JIG-1 --pr <url> --repo web`", "`jig publish JIG-1 --repo web`"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the body of one repo of two lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	renderBodies(&out, root, "JIG-1", verifydeliver.BodyReport{Branch: "jig/JIG-1", Covered: []string{"api", "web"}, Repos: []verifydeliver.RepoBody{api, web}})
	for _, want := range []string{
		"publish_body:", "branch: jig/JIG-1", "bodies[2]{repo,base,head,title,pr_body,review_notes}:",
		"  api,main,aaa111,", "  web,develop,bbb222,", "review-notes-web.md", "--pr <url> --repo <repo>", "pending",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the bodies of two repos lack %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "repo: ") {
		t.Errorf("the bodies of two repos print a repo fact beside the table:\n%s", out.String())
	}
}

// TestApprovedRows: an approval of one repo says its head; of several, the heads
// are a table and the facts say the branch alone.
func TestApprovedRows(t *testing.T) {
	t.Parallel()
	one := verifydeliver.ApproveReport{Branch: "jig/JIG-1", Provenance: "person", Repos: []verifydeliver.RepoHead{{Repo: "api", Head: "aaa111"}}}
	want := [][2]string{{"ticket", "JIG-1"}, {"repo", "api"}, {"branch", "jig/JIG-1"}, {"head", "aaa111"}, {"provenance", "person"}}
	if got := approvedRows("JIG-1", one); !reflect.DeepEqual(got, want) {
		t.Errorf("approval of one repo = %v, want %v", got, want)
	}
	two := one
	two.Repos = append(two.Repos, verifydeliver.RepoHead{Repo: "web", Head: "bbb222"})
	want = [][2]string{{"ticket", "JIG-1"}, {"branch", "jig/JIG-1"}, {"provenance", "person"}}
	if got := approvedRows("JIG-1", two); !reflect.DeepEqual(got, want) {
		t.Errorf("approval of two repos = %v, want %v", got, want)
	}
}

// TestJoinWords: words as prose.
func TestJoinWords(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"": "", "api": "api", "api,web": "api and web", "api,web,docs": "api, web and docs"} {
		var words []string
		if in != "" {
			words = strings.Split(in, ",")
		}
		if got := joinWords(words); got != want {
			t.Errorf("joinWords(%v) = %q, want %q", words, got, want)
		}
	}
}
