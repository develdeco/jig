package verifydeliver

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// doneSize is the change size on the publish-done line of ticket's journal,
// and fails the test when the ticket has no such line or it has no size.
func doneSize(t *testing.T, d Deps, ticket string) journal.ChangeSize {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			if l.Change == nil {
				t.Fatalf("publish-done line has no change size: %+v", l)
			}
			return *l.Change
		}
	}
	t.Fatal("the journal has no publish-done line")
	return journal.ChangeSize{}
}

// sizeByHand is the size of the change from base to head in dir, counted with
// plain git the way a person would: the files the branch changes since it left
// base, and the size of each at that point.
func sizeByHand(t *testing.T, dir, base, head string) journal.ChangeSize {
	t.Helper()
	mb := run(t, dir, "merge-base", base, head)
	names := run(t, dir, "diff", "--name-only", "--no-renames", mb, head)
	var size journal.ChangeSize
	for _, name := range strings.Fields(names) {
		size.Files++
		if out, err := gitx.Run(dir, "cat-file", "-s", mb+":"+name); err == nil {
			n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			size.Bytes += n
		}
	}
	return size
}

// TestPublishChangeSizeOfARecordedPR: a pull request the harness opened and
// jig recorded finishes the publish, and its publish-done line carries the
// size of the reviewed change.
func TestPublishChangeSizeOfARecordedPR(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	_, addEnv := useGithubHost(t, &d, "", "")
	addEnv("GH_STUB_HEAD_REF="+ticketBranch(fx.Ticket), "GH_STUB_HEAD_OID="+head)
	if _, err := PublishRecordPR(d, PublishOpts{Ticket: fx.Ticket}, "https://github.com/owner/fixture-repo/pull/7", nil); err != nil {
		t.Fatalf("PublishRecordPR: %v", err)
	}
	want := sizeByHand(t, fx.RepoDir, "origin/main", head)
	if want.Files < 1 {
		t.Fatalf("the fixture change has %+v, want at least a file", want)
	}
	if got := doneSize(t, d, fx.Ticket); got != want {
		t.Errorf("size = %+v, want %+v", got, want)
	}
}

// TestPublishChangeSizeOfATicketRecordedPerRepo: the second repo's pull
// request finishes a ticket of two repos, and the size on its publish-done
// line is the sum of both repos', not the second's alone.
func TestPublishChangeSizeOfATicketRecordedPerRepo(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	headAPI, headWeb := o.build(t, "api"), o.build(t, "web")
	o.reviewedClean(t)
	_, addEnv := useGithubHost(t, &o.d, "", "")
	addEnv("GH_STUB_HEAD_REF="+o.branch, "GH_STUB_HEAD_OID="+headAPI)
	if _, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "api"}, "https://github.com/owner/fixture-repo/pull/3", nil); err != nil {
		t.Fatalf("record api: %v", err)
	}
	addEnv("GH_STUB_HEAD_OID=" + headWeb)
	if _, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "web"}, "https://github.com/owner/fixture-repo/pull/4", nil); err != nil {
		t.Fatalf("record web: %v", err)
	}
	api := sizeByHand(t, o.d.Machine.Clones["api"], "origin/main", headAPI)
	web := sizeByHand(t, o.d.Machine.Clones["web"], "origin/main", headWeb)
	want := journal.ChangeSize{Files: api.Files + web.Files, Bytes: api.Bytes + web.Bytes}
	if want.Files < 2 {
		t.Fatalf("each repo changes a file, got %+v", want)
	}
	if got := doneSize(t, o.d, o.ticket); got != want {
		t.Errorf("size = %+v, want %+v (both repos)", got, want)
	}
}

// TestPublishChangeSizeOfARepoPublishThatFinishesTheTicket: a publish of the
// api alone, then of the web alone, which finishes the ticket: the publish-done
// line sums both repos.
func TestPublishChangeSizeOfARepoPublishThatFinishesTheTicket(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	headAPI, headWeb := o.build(t, "api"), o.build(t, "web")
	o.reviewedClean(t)
	useGithubHost(t, &o.d, "", "")
	if _, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true, Repo: "api"}); err != nil {
		t.Fatalf("Publish api: %v", err)
	}
	if _, done := prLines(t, o.d, o.ticket); done {
		t.Fatal("the ticket is published after its first repo")
	}
	if _, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true, Repo: "web"}); err != nil {
		t.Fatalf("Publish web: %v", err)
	}
	api := sizeByHand(t, o.d.Machine.Clones["api"], "origin/main", headAPI)
	web := sizeByHand(t, o.d.Machine.Clones["web"], "origin/main", headWeb)
	want := journal.ChangeSize{Files: api.Files + web.Files, Bytes: api.Bytes + web.Bytes}
	if got := doneSize(t, o.d, o.ticket); got != want || want.Files < 2 {
		t.Errorf("size = %+v, want %+v (both repos)", got, want)
	}
}

// TestPublishChangeSizeIsMeasuredFromOriginsBase: the base is origin's branch,
// not a local branch of the same name that has moved on.
func TestPublishChangeSizeIsMeasuredFromOriginsBase(t *testing.T) {
	t.Parallel()
	clone := sizedBranch(t, "12345678")
	// The local main has caught up with the work: against it nothing changed.
	run(t, clone, "branch", "-f", "main", "work")
	got, err := changeSize([]repoSpan{{dir: clone, base: "main", head: "work"}})
	if err != nil {
		t.Fatalf("changeSize: %v", err)
	}
	if got.Files != 3 || got.Bytes != 14 {
		t.Errorf("size = %+v, want 3 files and 14 bytes against origin's main", got)
	}
}
