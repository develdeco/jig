package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestPublishTrailerFromTheBranchMarkerReadsBackAsTheRender: the branch's
// jig.yaml sets publish: trailer:, `jig publish --body` renders a body that
// ends with it, the pull request a session opens on the host from that body is
// recorded by `--pr`, and its pr line says body_match matches. Without the
// trailer in the render, a pull request that carried it would read as differs,
// which is the mismatch the trailer exists to end. The clone's marker on disk
// sets another trailer, which the render must not take.
func TestPublishTrailerFromTheBranchMarkerReadsBackAsTheRender(t *testing.T) {
	publishTrailerReadsBack(t, true, "The older main's trailer.", "Opened by an agent.\r\nSecond line.  ", "\nOpened by an agent.\nSecond line.\n")
}

// TestPublishTrailerFromConfigWhenTheHeadHasNoMarkerReadsBackAsTheRender: the
// reviewed head commits no jig.yaml, so the trailer is Config's, the marker of
// the project on this machine (excluded from git here), as it is for every
// member repo of an orchestrator project; the body ends with it and the pull
// request opened from the render reads back as matching.
func TestPublishTrailerFromConfigWhenTheHeadHasNoMarkerReadsBackAsTheRender(t *testing.T) {
	publishTrailerReadsBack(t, false, "From the machine.\r\nSecond line.  ", "", "\nFrom the machine.\nSecond line.\n")
}

// publishTrailerReadsBack builds a ticket in a v3 store, renders its body and
// records the pull request opened from the render. When committed, the branch
// commits a jig.yaml with trailer; the marker on disk (what the machine reads)
// always sets diskTrailer. The rendered body must end with ending.
func publishTrailerReadsBack(t *testing.T, committed bool, diskTrailer, trailer, ending string) {
	t.Helper()
	// Not parallel: the pull-request host is the fake gh on the process's PATH.
	const (
		remote = "git@github.com:owner/fixture-repo.git"
		prURL  = "https://github.com/owner/fixture-repo/pull/7"
	)
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})

	// The branch's own jig.yaml sets the trailer: it is committed before the
	// ticket is built, so the reviewed head carries it.
	branchMarker := project.Marker{
		Jig: "1", Project: "fixture", Kind: project.KindProject,
		Repos:   []project.MarkerRepo{{Name: "fixture-repo", Remote: remote, Target: "main"}},
		Publish: &project.Publish{Trailer: trailer},
	}
	if committed {
		if err := project.WriteMarker(fx.RepoDir, branchMarker); err != nil {
			t.Fatal(err)
		}
		gitMust(t, fx.RepoDir, "add", project.MarkerFile)
		gitMust(t, fx.RepoDir, "commit", "-m", "chore: the project marker")
	}
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)
	head := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")

	// The fixture's store is v2, which takes nothing from a marker: make it
	// a v3 store. The clone's marker on disk (what the machine reads) names the
	// GitHub remote and sets diskTrailer; a committed one is told to git as
	// unchanged, and one the head lacks is excluded, so the clone stays clean.
	if err := os.Remove(filepath.Join(fx.StoreDir, "project.yaml")); err != nil {
		t.Fatal(err)
	}
	storeYAML := "schema_version: 3\nkeys:\n    JIG: everything in fixture\ntrackers: []\nplatform: platform/\nprojects: [fixture]\n"
	if err := os.WriteFile(filepath.Join(fx.StoreDir, "store.yaml"), []byte(storeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	diskMarker := branchMarker
	diskMarker.Publish = &project.Publish{Trailer: diskTrailer}
	if err := project.WriteMarker(fx.RepoDir, diskMarker); err != nil {
		t.Fatal(err)
	}
	if committed {
		gitMust(t, fx.RepoDir, "update-index", "--assume-unchanged", project.MarkerFile)
	} else {
		exclude := filepath.Join(fx.RepoDir, ".git", "info", "exclude")
		if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(project.MarkerFile + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	e = e.inDir(fx.RepoDir)

	// The fake gh is the pull-request host.
	t.Setenv("PATH", fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_STUB_LOG", filepath.Join(t.TempDir(), "gh.log"))
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))
	t.Setenv("GH_STUB_HEAD_REF", fx.Branch)
	t.Setenv("GH_STUB_HEAD_OID", head)

	flags := []string{"--store", fx.StoreDir}
	bodyOut, code := runMain(t, e, "", append([]string{"publish", fx.Ticket, "--body"}, flags...)...)
	if code != 0 {
		t.Fatalf("publish --body: exit = %d, want 0\n%s", code, bodyOut)
	}
	var title string
	for _, line := range strings.Split(bodyOut, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "title: "); ok {
			title = strings.Trim(v, `"`)
		}
	}
	if title == "" {
		t.Fatalf("publish --body names no title:\n%s", bodyOut)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	render, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil {
		t.Fatalf("read pr/fixture-repo.md: %v", err)
	}
	if !strings.Contains(string(render), "## Verification") || !strings.HasSuffix(string(render), "\n"+ending) {
		t.Fatalf("the rendered body does not end with the trailer after Verification and a blank line:\n%q", render)
	}

	// The pull request is opened from the render, as the host serves it.
	t.Setenv("GH_STUB_BODY", string(render))
	t.Setenv("GH_STUB_TITLE", title)
	out, prCode := runMain(t, e, "", append([]string{"publish", fx.Ticket, "--pr", prURL}, flags...)...)
	if prCode != 0 {
		t.Fatalf("publish --pr: exit = %d, want 0\n%s", prCode, out)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	var pr journal.Line
	for _, l := range lines {
		if l.Event == journal.EventPr {
			pr = l
		}
	}
	if pr.Event == "" || pr.URL != prURL {
		t.Fatalf("the journal has no pr line for %s: %+v", prURL, pr)
	}
	if pr.BodyMatch == journal.BodyMatches {
		return
	}
	t.Errorf("the pr line says body_match %q, want %q\n%s", pr.BodyMatch, journal.BodyMatches, out)
}
