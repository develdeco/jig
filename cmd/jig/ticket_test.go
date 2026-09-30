package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// TestTicketNewRecordsTitleOnLocalTracker covers the local tracker: jig
// ticket new writes the title into <ticket>/ticket.yaml, not only into the
// tracker's own tracker/ticket.md.
func TestTicketNewRecordsTitleOnLocalTracker(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(repo)

	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := Main(args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, out)
	}
	// A minted ticket has no work yet, and there are two ways to give it some:
	// a brief with slices, or a branch built outside jig, which needs neither.
	for _, want := range []string{"jig validate T-1", "jig gate T-1 --branch <name>"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig ticket new's output does not offer %q:\n%s", want, out)
		}
	}

	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}
	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "Fix the thing")
	}
}

// TestTicketNewOnGithubTrackerThenRequireTicketPasses reproduces the bug
// where a github-tracked ticket had no store folder: the github tracker's
// Mint creates none of its own (unlike the local tracker's
// tracker/ticket.md), so requireTicket used to fail for a GitHub-tracked
// ticket until someone wrote a brief under it by hand. jig ticket new now
// creates <ticket>/ticket.yaml, with the title, for every tracker, which
// creates that folder as a side effect.
func TestTicketNewOnGithubTrackerThenRequireTicketPasses(t *testing.T) {
	stubDir := fixture.GhStub(t)
	t.Setenv("JIG_HOME", t.TempDir())
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_STUB_LOG", filepath.Join(t.TempDir(), "gh.log"))
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))

	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(repo)

	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := Main(args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}
	data, err := os.ReadFile(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(data), "tracker: local", "tracker: github", 1)
	if rewritten == string(data) {
		t.Fatalf("project.yaml has no local tracker to replace:\n%s", data)
	}
	rewritten2 := strings.Replace(rewritten, "remote: "+repo, "remote: owner/repo", 1)
	if rewritten2 == rewritten {
		t.Fatalf("project.yaml has no repo remote %q to replace:\n%s", repo, rewritten)
	}
	if err := os.WriteFile(cfgs[0], []byte(rewritten2), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code != 0 || !strings.Contains(out, "#1") {
		t.Fatalf("jig ticket new on the github tracker: exit %d, want id #1:\n%s", code, out)
	}

	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := requireTicket(st, "#1"); err != nil {
		t.Fatalf("requireTicket(#1) after jig ticket new on the github tracker: %v", err)
	}
}

// commandTrackerMinting initializes a standalone store whose tracker is a
// command that always mints id, regardless of what already exists in the
// store - unlike the local tracker, which always mints the next free id and
// so can never collide or mint one jig cannot use - and returns a runner for
// jig commands against it and the store itself. It stands in for a tracker
// like github, whose id is known only once the ticket already exists there.
func commandTrackerMinting(t *testing.T, id string) (func(args ...string) (int, string), *store.Store) {
	t.Helper()
	t.Setenv("JIG_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(repo)

	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := Main(args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}

	stubDir := t.TempDir()
	script := filepath.Join(stubDir, "tracker.sh")
	content := "#!/bin/sh\ncat > /dev/null\necho '{\"id\": \"" + id + "\"}'\n"
	if runtime.GOOS == "windows" {
		script = filepath.Join(stubDir, "tracker.cmd")
		content = "@echo off\r\nfindstr \"^\" > nul\r\necho {\"id\": \"" + id + "\"}\r\n"
	}
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write tracker stub: %v", err)
	}
	data, err := os.ReadFile(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	withCommand := strings.Replace(string(data), "tracker: local", "tracker:\n    command: '"+script+"'", 1)
	if withCommand == string(data) {
		t.Fatalf("project.yaml has no local tracker to replace:\n%s", data)
	}
	if err := os.WriteFile(cfgs[0], []byte(withCommand), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return jig, st
}

// TestTicketNewRefusesPreExistingRecord covers a tracker whose id is known
// only once it has created the ticket (a command tracker here, standing in
// for github): if the store already has a ticket.yaml for the id it just
// minted - some earlier, unrelated write already claimed it - jig refuses
// rather than merging this title into that other ticket's record. The
// tracker-side ticket still exists by the time this refusal happens (the
// same unavoidable ordering as the reserved-id refusal), so the message
// says so and tells the operator not to mint again for it. The collision has
// its own message and help, not the generic record-write failure's.
func TestTicketNewRefusesPreExistingRecord(t *testing.T) {
	jig, st := commandTrackerMinting(t, "EXT-1")
	if err := st.CreateTicketRecord("EXT-1", store.Ticket{Title: "Pre-existing, unrelated ticket"}); err != nil {
		t.Fatalf("seed a pre-existing ticket.yaml for EXT-1: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(st.TicketDir("EXT-1"), "ticket.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code == 0 || !strings.Contains(out, "the command tracker minted EXT-1, but the store already has a ticket.yaml for EXT-1") {
		t.Fatalf("jig ticket new colliding with a pre-existing EXT-1 record: exit %d, want a non-zero exit naming the command tracker, EXT-1 and the collision:\n%s", code, out)
	}
	if !strings.Contains(out, "do not run") || !strings.Contains(out, "which ticket already claims this id") || !strings.Contains(out, "do not delete it") {
		t.Fatalf("jig ticket new colliding with a pre-existing EXT-1 record: exit %d, want help not to mint again, to inspect the claiming record and not to delete it:\n%s", code, out)
	}
	if record := st.TicketFilePath("EXT-1"); !strings.Contains(out, record) {
		t.Fatalf("jig ticket new colliding with a pre-existing EXT-1 record: help does not name the record to inspect, %s:\n%s", record, out)
	}
	if strings.Contains(out, "recording its title in the store failed") {
		t.Fatalf("jig ticket new colliding with a pre-existing EXT-1 record reported the generic write failure:\n%s", out)
	}

	after, err := os.ReadFile(filepath.Join(st.TicketDir("EXT-1"), "ticket.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the pre-existing ticket.yaml for EXT-1 changed after the refused mint:\nbefore %q\nafter  %q", before, after)
	}
}

// TestTicketNewNamesTheMintedTicketWhenRecordingItFails covers a store write
// that fails for a reason other than a collision (a regular file sits where
// the ticket's store folder must go): the tracker-side ticket exists by then,
// so the message names the tracker and the id, tells the operator not to mint
// again for it, and does not claim the id collides with another record.
func TestTicketNewNamesTheMintedTicketWhenRecordingItFails(t *testing.T) {
	jig, st := commandTrackerMinting(t, "EXT-1")
	if err := os.WriteFile(st.TicketDir("EXT-1"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code == 0 || !strings.Contains(out, "the command tracker minted EXT-1, but recording its title in the store failed") {
		t.Fatalf("jig ticket new with EXT-1's store folder blocked: exit %d, want a non-zero exit naming the command tracker, EXT-1 and the write failure:\n%s", code, out)
	}
	if !strings.Contains(out, "do not run") {
		t.Fatalf("jig ticket new with EXT-1's store folder blocked: want help not to mint again:\n%s", out)
	}
	if record := st.TicketFilePath("EXT-1"); !strings.Contains(out, record) {
		t.Fatalf("jig ticket new with EXT-1's store folder blocked: help does not name the record to write by hand, %s:\n%s", record, out)
	}
	if strings.Contains(out, "already has a ticket.yaml") {
		t.Fatalf("jig ticket new with EXT-1's store folder blocked reported a collision:\n%s", out)
	}
}

// TestTicketNewRecordsInAFolderThatHasNoRecord pins the other side of the
// collision rule: it is keyed on the record, not the folder. A minted id whose
// store folder already exists with no ticket.yaml (what the intake skill
// writes for a ticket that exists only in its tracker, and what a ticket
// minted before titles were recorded looks like) is not a collision: the
// record is created in it, and what the folder already held is left alone.
func TestTicketNewRecordsInAFolderThatHasNoRecord(t *testing.T) {
	jig, st := commandTrackerMinting(t, "EXT-1")
	briefPath := filepath.Join(st.TicketDir("EXT-1"), "brief.md")
	if err := os.MkdirAll(st.TicketDir("EXT-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# Written before the ticket was minted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, out := jig("ticket", "new", "--title", "Fix the thing"); code != 0 {
		t.Fatalf("jig ticket new over a folder with no ticket.yaml: exit %d, want the record created in it:\n%s", code, out)
	}

	rec, err := st.ReadTicket("EXT-1")
	if err != nil || rec.Title != "Fix the thing" {
		t.Fatalf("EXT-1's record = %+v, %v, want the new title", rec, err)
	}
	if brief, err := os.ReadFile(briefPath); err != nil || string(brief) != "# Written before the ticket was minted\n" {
		t.Fatalf("EXT-1's brief.md after ticket new = %q, %v, want it untouched", brief, err)
	}
}
