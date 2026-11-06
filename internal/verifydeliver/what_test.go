package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// bodyOf reads the pull request body PublishBody rendered for the one repo of
// a fixture.
func bodyOf(t *testing.T, d Deps, report BodyReport) string {
	t.Helper()
	if len(report.Repos) != 1 {
		t.Fatalf("report = %+v, want a body for the one repo", report)
	}
	data, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.Repos[0].PRBody)))
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	return string(data)
}

func writeWhatFile(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPublishBodyWhatIsAskedForGivenAndReused: until the session writes it, a
// body's What is the ticket's title and the report says pending with the file
// to write; `--what` stores the session's bullets in the ticket and the next
// render, given nothing, reuses them.
func TestPublishBodyWhatIsAskedForGivenAndReused(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	if err := d.Store.CreateTicketRecord(fx.Ticket, store.Ticket{Title: "Recorded title"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	gateToClean(t, fx, d)

	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	pending := report.Repos[0].What
	if pending.Status != WhatPending || pending.Request == "" || !filepath.IsAbs(pending.Request) {
		t.Fatalf("what = %+v, want pending with the absolute path of the file to write", pending)
	}
	if !strings.HasPrefix(pending.Request, d.Home) {
		t.Errorf("the request %s is outside the jig home %s, want it beside the evidence, never in the store", pending.Request, d.Home)
	}
	if body := bodyOf(t, d, report); !strings.Contains(body, "## What\n\n- Recorded title\n\n") {
		t.Fatalf("body = %q, want the ticket's title as the one bullet while the What is pending", body)
	}

	const bullets = "- Reviewers saw a body that told the build, not the change.\n- The body is now What, Demo and Verification.\n"
	file := writeWhatFile(t, pending.Request, bullets)
	given, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket, What: file})
	if err != nil {
		t.Fatalf("PublishBody --what: %v", err)
	}
	if got := given.Repos[0].What; got.Status != WhatGiven || got.Request != "" {
		t.Fatalf("what = %+v, want given with nothing left to ask", got)
	}
	want := "## What\n\n" + strings.TrimSpace(bullets) + "\n\n"
	if body := bodyOf(t, d, given); !strings.Contains(body, want) {
		t.Fatalf("body = %q, want it to hold %q", body, want)
	}
	stored, err := os.ReadFile(whatStorePath(d.Store, fx.Ticket, "fixture-repo"))
	if err != nil || strings.TrimSpace(string(stored)) != strings.TrimSpace(bullets) {
		t.Fatalf("the ticket's stored what = %q, %v, want the bullets given", stored, err)
	}

	reused, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody again: %v", err)
	}
	if got := reused.Repos[0].What; got.Status != WhatStored || got.Request != "" {
		t.Fatalf("what = %+v, want stored", got)
	}
	body := bodyOf(t, d, reused)
	if !strings.Contains(body, want) {
		t.Fatalf("body = %q, want the stored bullets reused: %q", body, want)
	}
	if got, wantH := bodyHeadings(body), []string{"## What", "## Verification"}; !reflect.DeepEqual(got, wantH) {
		t.Fatalf("sections = %v, want %v", got, wantH)
	}
}

// TestPublishWhatKeepsTheBodysSections: the bullets a session wrote cannot add
// a section to the body, whatever headings they hold or leave open.
func TestPublishWhatKeepsTheBodysSections(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	file := writeWhatFile(t, filepath.Join(t.TempDir(), "what.md"), "## Why\n\n- It was needed.\n\n```\nan open fence\n")
	report, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket, What: file})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	body := bodyOf(t, d, report)
	if got, want := bodyHeadings(body), []string{"## What", "## Verification"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %v, want %v:\n%s", got, want, body)
	}
	if !strings.Contains(body, "an open fence\n```\n\n## Verification") {
		t.Fatalf("body = %q, want the open fence closed before the next section", body)
	}
}

func TestLoadWhatFile(t *testing.T) {
	t.Parallel()

	if got, err := loadWhatFile(""); got != nil || err != nil {
		t.Fatalf("loadWhatFile(\"\") = %v, %v, want nil for no file", got, err)
	}
	dir := t.TempDir()
	_, err := loadWhatFile(filepath.Join(dir, "missing.md"))
	wantAxiCode(t, err, "WHAT_UNREADABLE")
	_, err = loadWhatFile(writeWhatFile(t, filepath.Join(dir, "empty.md"), " \r\n\r\n"))
	wantAxiCode(t, err, "WHAT_EMPTY")

	got, err := loadWhatFile(writeWhatFile(t, filepath.Join(dir, "ok.md"), "\r\n- one\r\n- two\r\n\r\n"))
	if err != nil || got == nil || *got != "- one\n- two" {
		t.Fatalf("loadWhatFile = %v, %v, want the bullets trimmed with LF endings", got, err)
	}
}

// TestLoadWhatForNeedsOneRepo: a what file is one repo's, so a ticket that
// builds in several says which with --repo.
func TestLoadWhatForNeedsOneRepo(t *testing.T) {
	t.Parallel()

	file := writeWhatFile(t, filepath.Join(t.TempDir(), "what.md"), "- one\n")
	several := []*publishTarget{
		{ticket: "T-1", repoName: "api", covered: []string{"api", "web"}},
		{ticket: "T-1", repoName: "web", covered: []string{"api", "web"}},
	}
	_, err := loadWhatFor(file, several)
	wantAxiCode(t, err, "VALIDATION_ERROR")
	if got, err := loadWhatFor("", several); got != nil || err != nil {
		t.Fatalf("loadWhatFor with no file = %v, %v, want nil", got, err)
	}
	if got, err := loadWhatFor(file, several[:1]); err != nil || got == nil {
		t.Fatalf("loadWhatFor for one repo = %v, %v, want the bullets", got, err)
	}
}
