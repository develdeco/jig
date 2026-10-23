package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// publishSection is the gate and publish section `jig review` and `jig publish`
// register for `jig here`.
func publishSection(t *testing.T) hereSection {
	t.Helper()
	for _, s := range hereSections() {
		if s.Name == "publish" {
			if s.Blocks == nil || s.JSON == nil || s.Order != 330 {
				t.Fatalf("the publish section is not registered as expected: %+v", s)
			}
			return s
		}
	}
	t.Fatal("no publish section is registered")
	return hereSection{}
}

// TestHerePublishSectionShowsWhereThePublishStands: the section says nothing
// where nothing was located or recorded, and otherwise the publish the journal
// holds - the head a body was rendered for, a person's approval, the pull
// request and whether the publish finished - in both forms `jig here` prints.
func TestHerePublishSectionShowsWhereThePublishStands(t *testing.T) {
	t.Parallel()
	section := publishSection(t)
	r := newVerifyRig(t)

	for name, c := range map[string]hereCtx{
		"no store":         {e: r.e},
		"no ticket":        {e: r.e, Store: r.st},
		"nothing recorded": hereVerifyCtx(r, ""),
	} {
		blocks, err := section.Blocks(c)
		if err != nil || len(blocks) != 0 {
			t.Errorf("%s: blocks = %v, %v, want none", name, blocks, err)
		}
		if v, err := section.JSON(c); err != nil || v != nil {
			t.Errorf("%s: JSON = %v, %v, want nothing", name, v, err)
		}
	}

	head := strings.Repeat("c", 40)
	const prURL = "https://github.com/owner/fixture-repo/pull/9"
	for _, l := range []journal.Line{
		{Event: journal.EventPrBody, Commit: head, Branch: "jig/" + r.fx.Ticket},
		{Event: journal.EventPublishApproved, Commit: head, Provenance: "person"},
		{Event: journal.EventPr, Outcome: "recorded", Commit: head, URL: prURL},
		{Event: journal.EventPublishDone},
	} {
		if err := journal.Append(r.st, r.fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}

	c := hereVerifyCtx(r, "")
	blocks, err := section.Blocks(c)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(blocks, "\n")
	for _, want := range []string{"publish:", "body_for: " + shortSHA(head), "approved_for: " + shortSHA(head), prURL, "published: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the section lacks %q:\n%s", want, out)
		}
	}
	v, err := section.JSON(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var s verifydeliver.Status
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse the section's JSON %s: %v", raw, err)
	}
	if s.Ticket != r.fx.Ticket || s.BodyFor != head || s.ApprovedFor != head || s.PR != prURL || !s.Published {
		t.Errorf("JSON status = %+v, want the ticket's body, approval, pull request and publish", s)
	}

	whole, code := r.run("here")
	if code != 0 {
		t.Fatalf("jig here exit = %d\n%s", code, whole)
	}
	if !strings.Contains(whole, prURL) {
		t.Errorf("jig here lacks the pull request:\n%s", whole)
	}
}
