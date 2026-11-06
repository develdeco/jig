package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPublishBodyAsksForTheWhatAndTakesItThroughMain: `jig publish --body`
// reports what: pending with the file to write and says how to give it,
// `--what <file>` stores the bullets in the ticket, and the next --body, given
// nothing, reports them stored and renders the same What.
func TestPublishBodyAsksForTheWhatAndTakesItThroughMain(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	buildFixtureTicket(t, fx)
	gateCleanThroughMain(t, e, fx)

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--body", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "what: pending") || !strings.Contains(out, "what_request: ") {
		t.Fatalf("publish --body: exit = %d, want 0 and what: pending with the file to write\n%s", code, out)
	}
	if !strings.Contains(out, "--what") {
		t.Errorf("publish --body output does not say how to give the What:\n%s", out)
	}
	var request string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "what_request: "); ok {
			request = strings.Trim(rest, `"`)
			request = strings.ReplaceAll(request, `\\`, `\`)
		}
	}
	if request == "" || !filepath.IsAbs(request) {
		t.Fatalf("what_request = %q, want an absolute path\n%s", request, out)
	}
	if err := os.WriteFile(request, []byte("- Readers get the change, not the build.\n- A second bullet.\n"), 0o644); err != nil {
		t.Fatalf("write the what file at the path the report named: %v", err)
	}

	out, code = runMain(t, e, "", "publish", fx.Ticket, "--body", "--what", request, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "what: given") || strings.Contains(out, "what_request") {
		t.Fatalf("publish --body --what: exit = %d, want 0 and what: given\n%s", code, out)
	}
	out, code = runMain(t, e, "", "publish", fx.Ticket, "--body", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "what: stored") {
		t.Fatalf("publish --body again: exit = %d, want 0 and what: stored\n%s", code, out)
	}
	body, err := os.ReadFile(filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))
	if err != nil || !strings.Contains(string(body), "## What\n\n- Readers get the change, not the build.\n- A second bullet.\n\n## Verification") {
		t.Fatalf("pr body = %q (err %v), want the stored bullets as the What", body, err)
	}

	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "publish", fx.Ticket, "--body", "--what", empty, "--store", fx.StoreDir); code == 0 || !strings.Contains(out, "WHAT_EMPTY") {
		t.Errorf("publish --body --what <empty file>: exit = %d, want a WHAT_EMPTY refusal\n%s", code, out)
	}
}

// TestWhatRowsSayWhereTheWhatStands: none for a report with no What, the
// status and for a pending one the file to write, a repo's name in each row
// when there are several.
func TestWhatRowsSayWhereTheWhatStands(t *testing.T) {
	t.Parallel()

	if rows := whatRows(nil); rows != nil {
		t.Errorf("rows for no What = %v, want none", rows)
	}
	one := whatRows(map[string]verifydeliver.WhatReport{"api": {Status: verifydeliver.WhatPending, Request: "/home/what/api.md"}})
	if want := [][2]string{{"what", "pending"}, {"what_request", "/home/what/api.md"}}; !reflect.DeepEqual(one, want) {
		t.Errorf("rows = %v, want %v", one, want)
	}
	several := whatRows(map[string]verifydeliver.WhatReport{
		"web": {Status: verifydeliver.WhatStored},
		"api": {Status: verifydeliver.WhatPending, Request: "/home/what/api.md"},
	})
	want := [][2]string{{"what_api", "pending"}, {"what_request_api", "/home/what/api.md"}, {"what_web", "stored"}}
	if !reflect.DeepEqual(several, want) {
		t.Errorf("rows = %v, want %v", several, want)
	}
}
