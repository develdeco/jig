package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestKeepsClaimBlockedMergedRefusedUnattendedWarnedInteractive: a ticket
// whose merged blocker has not published is refused a claim when nobody is
// watching (CLAIM_BLOCKED_MERGED, exit 1, nothing written) and warned about,
// and claimed all the same, when a person is; another session finds the slice
// held and --take is the way past.
func TestKeepsClaimBlockedMergedRefusedUnattendedWarnedInteractive(t *testing.T) {
	t.Parallel()
	_, st, e := workFixture(t)

	dir := st.TicketDir("JIG-2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"ticket.yaml": "schema_version: 1\ntitle: Follows JIG-1\nblocked_by:\n  - ticket: JIG-1\n    kind: merged\n",
		"brief.md":    "# Follows JIG-1\n\n## Goal\n\nBuild x and y.\n",
		"slices.yaml": "slices:\n" +
			"  - id: x\n    workspace: alpha\n    goal: Build x.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n" +
			"  - id: y\n    workspace: alpha\n    goal: Build y.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	approveBrief(t, st, "JIG-2")

	out, code := runMain(t, e, "", "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if code != 1 || !strings.Contains(out, "code: CLAIM_BLOCKED_MERGED") || !strings.Contains(out, "JIG-1") {
		t.Fatalf("unattended claim: exit %d, want 1 with CLAIM_BLOCKED_MERGED naming JIG-1\n%s", code, out)
	}
	if lines := linesOf(t, st, "JIG-2", journal.EventClaim); len(lines) != 0 {
		t.Errorf("a refused claim left claim lines: %+v", lines)
	}

	out, code = runMain(t, e, "", "claim", "JIG-2", "--slice", "x", "--session", "s-1")
	if code != 0 {
		t.Fatalf("interactive claim: exit %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"  slice: x\n", "  result: claimed\n", "  state: building\n", "  session: s-1\n", "warnings[1]{code,message,help}:\n", "CLAIM_BLOCKED_MERGED,"} {
		if !strings.Contains(out, want) {
			t.Errorf("interactive claim output lacks %q:\n%s", want, out)
		}
	}
	if lines := linesOf(t, st, "JIG-2", journal.EventClaim); len(lines) != 1 || lines[0].Slice != "x" || lines[0].Session != "s-1" || lines[0].Unattended {
		t.Errorf("claim lines = %+v, want one for slice x by s-1, attended", lines)
	}

	out, code = runMain(t, e, "", "claim", "JIG-2", "--slice", "x", "--session", "s-2")
	if code != 1 || !strings.Contains(out, "code: SLICE_HELD") || !strings.Contains(out, "s-1") {
		t.Errorf("a second session's claim: exit %d, want 1 with SLICE_HELD naming s-1\n%s", code, out)
	}
	out, code = runMain(t, e, "", "claim", "JIG-2", "--slice", "x", "--session", "s-2", "--take")
	if code != 0 || !strings.Contains(out, "result: taken-over") || !strings.Contains(out, "took_over_from: s-1") {
		t.Errorf("claim --take: exit %d, want 0 taken over from s-1\n%s", code, out)
	}
}
