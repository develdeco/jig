package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/repohost"
)

// approved is a publish-approved journal line.
func approved(repo, commit, provenance, who string) journal.Line {
	return journal.Line{Event: journal.EventPublishApproved, Repo: repo, Commit: commit, Provenance: provenance, Summary: who}
}

func TestPureRuleApprovalOfIsTheLastLineForTheHeadAndRepo(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	for _, c := range []struct {
		name         string
		lines        []journal.Line
		repo, head   string
		wantApproval string
		wantIdentity string
	}{
		{"no line", nil, "r", head, approvalNone, ""},
		{"a person's", []journal.Line{approved("r", head, "person", "Dana <d@example.test>")}, "r", head, approvalPerson, "Dana"},
		{"an agent's, or any other provenance", []journal.Line{approved("r", head, "agent", "Bot <b@example.test>")}, "r", head, approvalAgent, "Bot"},
		{"a name with no email is kept whole", []journal.Line{approved("r", head, "person", "Dana")}, "r", head, approvalPerson, "Dana"},
		{"another head", []journal.Line{approved("r", other, "person", "Dana")}, "r", head, approvalNone, ""},
		{"another repo", []journal.Line{approved("other", head, "person", "Dana")}, "r", head, approvalNone, ""},
		{"no head approves nothing", []journal.Line{approved("r", "", "person", "Dana")}, "r", "", approvalNone, ""},
		{"the last line wins, an agent's after a person's", []journal.Line{approved("r", head, "person", "Dana"), approved("r", head, "agent", "Bot")}, "r", head, approvalAgent, "Bot"},
		{"the last line wins, a person's after an agent's", []journal.Line{approved("r", head, "agent", "Bot"), approved("r", head, "person", "Dana")}, "r", head, approvalPerson, "Dana"},
		{"a line of another event is no approval", []journal.Line{{Event: journal.EventPr, Repo: "r", Commit: head, Provenance: "person"}}, "r", head, approvalNone, ""},
	} {
		approval, identity := approvalOf(c.lines, c.repo, c.head)
		if approval != c.wantApproval || identity != c.wantIdentity {
			t.Errorf("%s: approvalOf = (%q, %q), want (%q, %q)", c.name, approval, identity, c.wantApproval, c.wantIdentity)
		}
	}
}

func TestPureRuleApprovedByPersonIsTheLastLineForTheHead(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	for _, c := range []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"no line", nil, false},
		{"a person's", []journal.Line{approved("", head, "person", "")}, true},
		{"an agent's", []journal.Line{approved("", head, "agent", "")}, false},
		{"a person's for another head", []journal.Line{approved("", strings.Repeat("b", 40), "person", "")}, false},
		{"an agent's after a person's", []journal.Line{approved("", head, "person", ""), approved("", head, "agent", "")}, false},
		{"a person's after an agent's", []journal.Line{approved("", head, "agent", ""), approved("", head, "person", "")}, true},
	} {
		if got := approvedByPerson(c.lines, head); got != c.want {
			t.Errorf("%s: approvedByPerson = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPureRuleNotesRenderedForTheReviewedRoundAndApproval(t *testing.T) {
	t.Parallel()
	const head = "aaaa"
	body := func(commit string) journal.Line {
		return journal.Line{Event: journal.EventPrBody, Repo: "r", Commit: commit}
	}
	round := journal.Line{Event: journal.EventGateRound}
	clean := journal.Line{Event: journal.EventGateClean}
	for _, c := range []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"no body", []journal.Line{round, clean}, false},
		{"a body after the clean round at the head", []journal.Line{round, clean, body(head)}, true},
		{"a body of an earlier head", []journal.Line{round, clean, body("bbbb")}, false},
		{"the latest body decides", []journal.Line{body("bbbb"), round, clean, body(head)}, true},
		{"a round closed after the body, at the same head", []journal.Line{round, clean, body(head), round, clean}, false},
		{"a round line alone after the body", []journal.Line{body(head), round}, false},
		{"an approval of the head after the body", []journal.Line{body(head), approved("r", head, "person", "Dana")}, false},
		{"an approval of the head before the body", []journal.Line{approved("r", head, "person", "Dana"), body(head)}, true},
		{"an approval of another head after the body", []journal.Line{body(head), approved("r", "bbbb", "person", "Dana")}, true},
		{"an approval of another repo after the body", []journal.Line{body(head), approved("other", head, "person", "Dana")}, true},
		{"a body of another repo", []journal.Line{{Event: journal.EventPrBody, Repo: "other", Commit: head}}, false},
	} {
		if got := notesRenderedFor(c.lines, "r", head); got != c.want {
			t.Errorf("%s: notesRenderedFor = %v, want %v", c.name, got, c.want)
		}
	}
	// An empty reviewed head is approved by nothing: an approval line for no
	// commit does not reopen the notes.
	if !notesRenderedFor([]journal.Line{body(""), approved("r", "", "person", "Dana")}, "r", "") {
		t.Error("an approval of no head made the notes of no head stale")
	}
}

func TestPureRulePullRequestURLIsOneCanonicalSpelling(t *testing.T) {
	t.Parallel()
	const canonical = "https://github.com/owner/fixture-repo/pull/7"
	for _, spelled := range []string{
		canonical,
		canonical + "/",
		canonical + "/files",
		canonical + "#issuecomment-1",
		"http://github.com/owner/fixture-repo/pull/7",
		"https://github.com/owner/fixture-repo/pull/7/commits",
	} {
		ref, err := repohost.ParsePRURL(spelled)
		if err != nil {
			t.Fatalf("ParsePRURL %s: %v", spelled, err)
		}
		if got := canonicalPRURL(ref); got != canonical {
			t.Errorf("canonicalPRURL(%s) = %s, want %s", spelled, got, canonical)
		}
	}
	a := repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "fixture-repo", Number: 7}
	for _, c := range []struct {
		name string
		b    repohost.PRRef
		want bool
	}{
		{"the same", a, true},
		{"the host, owner and repo in another case", repohost.PRRef{Host: "GitHub.com", Owner: "Owner", Repo: "Fixture-Repo", Number: 7}, true},
		{"another number", repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "fixture-repo", Number: 8}, false},
		{"another owner", repohost.PRRef{Host: "github.com", Owner: "someone", Repo: "fixture-repo", Number: 7}, false},
		{"another repo", repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "other", Number: 7}, false},
		{"another host", repohost.PRRef{Host: "git.example", Owner: "owner", Repo: "fixture-repo", Number: 7}, false},
	} {
		if got := samePullRequest(a, c.b); got != c.want {
			t.Errorf("%s: samePullRequest = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPureRuleConfirmationNamesTheBranchTheOpenPullRequestAndTheHost(t *testing.T) {
	t.Parallel()
	ship := func(repo, branch, openURL string, host repohost.Host) *shipment {
		return &shipment{pt: &publishTarget{repoName: repo, branch: branch}, openPR: repohost.OpenPR{URL: openURL}, host: host}
	}
	var host repohost.Host = failingReadHost{}

	branch, open, has := confirmation([]*shipment{ship("a", "t-1", "https://x/pull/1", host)})
	if branch != "t-1" || open != "https://x/pull/1" || !has {
		t.Errorf("one repo with an open pull request and a host: (%q, %q, %v)", branch, open, has)
	}
	branch, open, has = confirmation([]*shipment{ship("a", "t-1", "", nil)})
	if branch != "t-1" || open != "" || has {
		t.Errorf("one repo with neither: (%q, %q, %v)", branch, open, has)
	}
	branch, open, has = confirmation([]*shipment{ship("a", "t-1", "https://x/pull/1", nil), ship("b", "t-1", "https://x/pull/2", nil)})
	if branch != "t-1 in a and b" || open != "" || has {
		t.Errorf("two repos, no host: (%q, %q, %v), want the branch with the repos and no one pull request", branch, open, has)
	}
	branch, _, has = confirmation([]*shipment{ship("a", "t-1", "", nil), ship("b", "t-1", "", host), ship("c", "t-1", "", nil)})
	if branch != "t-1 in a, b and c" || !has {
		t.Errorf("three repos, one with a host: (%q, %v), want the branch in a, b and c with a host", branch, has)
	}
}
