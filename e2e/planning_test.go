package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// ticketIDRE reads the id out of the ticket block jig ticket new prints.
var ticketIDRE = regexp.MustCompile(`(?m)^  id: (\S+)$`)

// mintTicket mints a ticket through the CLI, as a person would, and returns
// its id.
func mintTicket(t *testing.T, env jigEnv, dir, title, body string) string {
	t.Helper()
	args := []string{"ticket", "new", "--title", title}
	if body != "" {
		args = append(args, "--body", body)
	}
	r := runJig(t, env, dir, args...)
	if r.Code != 0 {
		t.Fatalf("jig ticket new %q exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", title, r.Code, r.Stdout, r.Stderr)
	}
	m := ticketIDRE.FindStringSubmatch(r.Stdout)
	if m == nil {
		t.Fatalf("jig ticket new printed no id:\n%s", r.Stdout)
	}
	return m[1]
}

// recallJSON runs jig recall --json over the words and reads the report.
func recallJSON(t *testing.T, env jigEnv, dir string, words ...string) recall.Report {
	t.Helper()
	r := runJig(t, env, dir, append(append([]string{"recall"}, words...), "--json")...)
	if r.Code != 0 {
		t.Fatalf("jig recall exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	var rep recall.Report
	if err := json.Unmarshal([]byte(r.Stdout), &rep); err != nil {
		t.Fatalf("jig recall --json is not one JSON object: %v\n%s", err, r.Stdout)
	}
	return rep
}

// writePlanTicket lays a valid ticket into the store: a brief, one slice in
// the fixture alpha workspace, and the blockers given (merged ones).
func writePlanTicket(t *testing.T, st *store.Store, id, title string, blockedBy ...string) {
	t.Helper()
	dir := st.TicketDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf("schema_version: 1\ntitle: %s\n", title)
	if len(blockedBy) > 0 {
		record += "blocked_by:\n"
		for _, b := range blockedBy {
			record += fmt.Sprintf("  - ticket: %s\n    kind: merged\n", b)
		}
	}
	files := map[string]string{
		"ticket.yaml": record,
		"brief.md":    "# " + title + "\n\n## Goal\n\nBuild it.\n",
		"slices.yaml": "slices:\n  - id: x\n    workspace: alpha\n    goal: Build it.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n",
	}
	for name, content := range files {
		writeFileOrFatal(t, filepath.Join(dir, name), []byte(content))
	}
}

// TestRecallNamesDuplicate: an open ticket whose title or body comes near the
// words asked about is named, with how near, and a ticket about something
// else is not; the answer is the same in blocks and in JSON.
func TestRecallNamesDuplicate(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	retry := mintTicket(t, env, fx.StoreDir, "Add retry with backoff to the fetch client", "The fetch client gives up on the first network error.")
	settings := mintTicket(t, env, fx.StoreDir, "Redesign the settings page", "Group the options by area.")

	rep := recallJSON(t, env, fx.StoreDir, "retry", "fetch", "client", "with", "backoff")
	if len(rep.Duplicates) != 1 || rep.Duplicates[0].Ticket != retry {
		t.Fatalf("duplicates = %+v, want exactly %s", rep.Duplicates, retry)
	}
	if d := rep.Duplicates[0]; d.Score < 0.5 || d.In != "title" || d.State != "new" {
		t.Errorf("duplicate = %+v, want a score of at least 0.5 against the title of a ticket with no slices yet", d)
	}
	open := map[string]string{}
	for _, tk := range rep.Tickets {
		open[tk.ID] = tk.State
	}
	if open[retry] != "new" || open[settings] != "new" || open[fx.Ticket] == "" {
		t.Errorf("open tickets = %v, want %s, %s and %s", open, retry, settings, fx.Ticket)
	}

	// A body that says it in other words is found by the body.
	rep = recallJSON(t, env, fx.StoreDir, "options", "grouped")
	if len(rep.Duplicates) != 0 {
		t.Errorf("words that share too little with %s: duplicates = %+v, want none", settings, rep.Duplicates)
	}
	rep = recallJSON(t, env, fx.StoreDir, "group", "the", "options", "by", "area")
	if len(rep.Duplicates) != 1 || rep.Duplicates[0].Ticket != settings || rep.Duplicates[0].In != "body" {
		t.Errorf("duplicates = %+v, want %s by its body", rep.Duplicates, settings)
	}

	// Words about something else name nothing.
	if rep = recallJSON(t, env, fx.StoreDir, "quantum", "teleportation", "parser"); len(rep.Duplicates) != 0 {
		t.Errorf("unrelated words: duplicates = %+v, want none", rep.Duplicates)
	}

	// Blocks carry the same finding and end in one help block.
	r := runJig(t, env, fx.StoreDir, "recall", "retry", "with", "backoff", "for", "the", "fetch", "client")
	if r.Code != 0 {
		t.Fatalf("jig recall exit = %d, want 0\n%s", r.Code, r.Stdout)
	}
	for _, want := range []string{"recall:\n", "  query: ", "duplicates[1]{ticket,score,in,state,title}:\n", "  " + retry + ",", "help["} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig recall stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Contains(r.Stdout, "  "+settings+",0.") {
		t.Errorf("jig recall names %s as a duplicate:\n%s", settings, r.Stdout)
	}
}

// TestRecallSuppliesTheStoreAroundTheQuery: besides the open tickets and the
// edges between them, a report carries the fog of each chart, the platform
// documents with the lines that mention the query, and the answers of the
// ledger entries that share a word with it.
func TestRecallSuppliesTheStoreAroundTheQuery(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	first := mintTicket(t, env, fx.StoreDir, "Add retry to uploads", "")
	second := mintTicket(t, env, fx.StoreDir, "Cap the upload size", "")
	// The second waits on the first.
	writeFileOrFatal(t, filepath.Join(fx.StoreDir, "tickets", second, "ticket.yaml"),
		[]byte(fmt.Sprintf("schema_version: 1\ntitle: Cap the upload size\nblocked_by:\n  - ticket: %s\n    kind: stacked\n", first)))
	if err := os.MkdirAll(filepath.Join(fx.StoreDir, "charts", "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(fx.StoreDir, "charts", "uploads", "map.md"), []byte(
		"# Uploads\n\n## Destination\n\nUploads survive a flaky network.\n\n## Not yet specified (fog)\n\n- Whether a cancelled upload resumes\n- Where partial files live,\n  and for how long\n\n## Out of scope\n\n- Resumable protocols\n"))
	writeFileOrFatal(t, filepath.Join(fx.StoreDir, "platform", "errors.md"), []byte(
		"# Error codes\n\nEvery refusal carries a code.\nA retry may follow only a NETWORK code.\n"))
	writeFileOrFatal(t, filepath.Join(fx.StoreDir, "ledger.md"), []byte(
		"# Ledger\n\n## JIG-7 - Add retry to the sync client\n\nDelivered 2 slice(s).\n\n**Answers:**\n- q-001: How often? -> Three times, with backoff.\n\n## JIG-8 - Rename the flags\n\nDelivered 1 slice(s).\n\n**Answers:**\nnone\n\n## JIG-9 - Tone of the greeting\n\nDelivered 1 slice(s).\n\n**Answers:**\n- q-001: Formal or casual? -> Casual.\n"))

	rep := recallJSON(t, env, fx.StoreDir, "retry", "network")
	if len(rep.Edges) != 1 || rep.Edges[0].Ticket != second || rep.Edges[0].BlockedBy != first || rep.Edges[0].Kind != "stacked" || rep.Edges[0].Dangling {
		t.Errorf("edges = %+v, want %s stacked on %s", rep.Edges, second, first)
	}
	if len(rep.Fog) != 1 || rep.Fog[0].Chart != "uploads" || len(rep.Fog[0].Items) != 2 ||
		rep.Fog[0].Items[1] != "Where partial files live, and for how long" {
		t.Errorf("fog = %+v, want the two items of the uploads fog section", rep.Fog)
	}
	if len(rep.Platform) != 1 || rep.Platform[0].Path != "platform/errors.md" || rep.Platform[0].Title != "Error codes" {
		t.Fatalf("platform = %+v, want platform/errors.md titled Error codes", rep.Platform)
	}
	// All of the document fits the head, so no line is repeated as a match.
	if len(rep.Platform[0].Head) != 3 || len(rep.Platform[0].Matches) != 0 {
		t.Errorf("platform doc lines = %+v, want its three lines as the head", rep.Platform[0])
	}
	if len(rep.Answers) != 1 || rep.Answers[0].Ticket != "JIG-7" || len(rep.Answers[0].Answers) != 1 ||
		rep.Answers[0].Answers[0].ID != "q-001" || !strings.Contains(rep.Answers[0].Answers[0].Text, "Three times") {
		t.Errorf("answers = %+v, want the JIG-7 answer alone: the query names retry", rep.Answers)
	}

	// Without a query the latest entries that have answers come, newest first.
	rep = recallJSON(t, env, fx.StoreDir)
	if len(rep.Answers) != 2 || rep.Answers[0].Ticket != "JIG-9" || rep.Answers[1].Ticket != "JIG-7" {
		t.Errorf("answers without a query = %+v, want JIG-9 then JIG-7", rep.Answers)
	}
	if len(rep.Duplicates) != 0 {
		t.Errorf("duplicates without a query = %+v, want none", rep.Duplicates)
	}

	// Blocks carry the same sections.
	r := runJig(t, env, fx.StoreDir, "recall")
	for _, want := range []string{"tickets[", "edges[1]{ticket,blocked_by,kind,blocker}:\n", "fog[2]{chart,item}:\n", "platform[1]{path,title,bytes}:\n", "answers[2]{ticket,title,id,text}:\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig recall stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
}

// TestValidateRefusesDanglingEdge: a ticket is not valid while an edge on the
// way to what it waits for is cut: its own blocked_by naming a ticket the
// store does not have, or one of an upstream ticket naming one. Mending the
// edge makes the ticket valid again.
func TestValidateRefusesDanglingEdge(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	// JIG-2 waits on JIG-9, which the store lacks; JIG-3 waits on JIG-2.
	writePlanTicket(t, st, "JIG-2", "Needs a missing ticket", "JIG-9")
	writePlanTicket(t, st, "JIG-3", "Needs JIG-2", "JIG-2")

	r := runJig(t, env, fx.StoreDir, "validate", "JIG-2")
	if r.Code == 0 || !strings.Contains(r.Stdout, "code: VALIDATION_ERROR") || !strings.Contains(r.Stdout, "JIG-9") || strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("validate of the ticket with a dangling edge: exit %d, want a refusal naming JIG-9\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.StoreDir, "validate", "JIG-3")
	if r.Code == 0 || !strings.Contains(r.Stdout, "upstream ticket JIG-2") || !strings.Contains(r.Stdout, "JIG-9") {
		t.Fatalf("validate of the ticket behind the dangling edge: exit %d, want a refusal naming JIG-2 and JIG-9\n%s", r.Code, r.Stdout)
	}
	for _, id := range []string{"JIG-2", "JIG-3"} {
		if _, ok, err := st.ReadApproval(id); err != nil || ok {
			t.Errorf("%s was refused and has an approval: %v, %v", id, ok, err)
		}
	}

	// Mend the edge: the upstream ticket no longer waits on a missing one.
	writePlanTicket(t, st, "JIG-2", "Needs nothing")
	r = runJig(t, env, fx.StoreDir, "validate", "JIG-3")
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "valid: yes\n") {
		t.Fatalf("validate after mending the edge: exit %d, want valid\n%s", r.Code, r.Stdout)
	}
}

// TestBriefEditUnapprovesClaim: validating a brief approves it, so an
// unattended claim goes through; once the brief is edited the approval no
// longer fits, the claim is refused (CLAIM_NOT_APPROVED) and nothing is
// written, and validating again approves the brief as it now is.
func TestBriefEditUnapprovesClaim(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	// Before the validation there is no approval to claim on.
	r := runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--unattended", "--session", "s-1")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_NOT_APPROVED") || !strings.Contains(r.Stdout, "is not approved") {
		t.Fatalf("claim before validate: exit %d, want 1 with CLAIM_NOT_APPROVED\n%s", r.Code, r.Stdout)
	}

	r = runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "valid: yes\n") || !strings.Contains(r.Stdout, "  result: recorded\n") {
		t.Fatalf("validate: exit %d, want valid with the approval recorded\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--unattended", "--session", "s-1")
	if r.Code != 0 || !strings.Contains(r.Stdout, "  result: claimed\n") {
		t.Fatalf("claim after validate: exit %d, want 0 claimed\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--session", "s-1", "--release")
	if r.Code != 0 {
		t.Fatalf("release: exit %d, want 0\n%s", r.Code, r.Stdout)
	}

	// The brief gains a section; the slices still trace to the sections they
	// were cut from, so only the approval no longer fits.
	briefPath := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "brief.md")
	writeFileOrFatal(t, briefPath, append(readFileOrFatal(t, briefPath), []byte("\n## Addendum\n\nAlso mind the empty range.\n")...))
	r = runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--unattended", "--session", "s-1")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_NOT_APPROVED") || !strings.Contains(r.Stdout, "changed after it was approved") {
		t.Fatalf("claim after the brief edit: exit %d, want 1 with CLAIM_NOT_APPROVED\n%s", r.Code, r.Stdout)
	}
	if got := sliceState(t, st, fx.Ticket, "a"); got != "queued" {
		t.Errorf("after the refused claim slice a is %s, want queued", got)
	}
	var claims int
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventClaim {
			claims++
		}
	}
	if claims != 1 {
		t.Errorf("the journal has %d claim lines, want only the first one", claims)
	}

	// Validating again approves the brief as it is now.
	r = runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.Contains(r.Stdout, "  result: recorded\n") {
		t.Fatalf("validate after the edit: exit %d, want valid with a new approval\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a", "--unattended", "--session", "s-1")
	if r.Code != 0 {
		t.Fatalf("claim after the second validate: exit %d, want 0\n%s", r.Code, r.Stdout)
	}
}

// TestValidateRecordsTheApproval: a valid brief is approved by the person
// whose identity git resolves in the store, with who vouched for it, an
// approval line in the journal and the record pushed; validating an
// unchanged brief again records nothing more, and a machine with no identity
// is refused with the store untouched.
func TestValidateRecordsTheApproval(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withoutIdentity()
	gitLog(t, fx.StoreDir, "config", "user.name", storePersonName)
	gitLog(t, fx.StoreDir, "config", "user.email", storePersonEmail)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	base := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "valid: yes\n") {
		t.Fatalf("validate: exit %d, want valid\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := st.ReadApproval(fx.Ticket)
	if err != nil || !ok {
		t.Fatalf("approval after validate: %v, %v", ok, err)
	}
	if a.By != storePersonName || a.Email != storePersonEmail || a.BriefSHA256 != sum || a.At == "" {
		t.Errorf("approval = %+v, want %s <%s> for the brief %s", a, storePersonName, storePersonEmail, sum)
	}
	if a.Provenance != "agent" {
		t.Errorf("approval provenance = %q, want agent: the command had no terminal on stdin", a.Provenance)
	}
	var line journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventApproval {
			line = l
		}
	}
	if line.Provenance != "agent" || line.Ref != sum[:12] || !strings.Contains(line.Summary, storePersonEmail) {
		t.Errorf("approval line = %+v, want provenance agent, ref %s and the approver", line, sum[:12])
	}
	requireStorePerson(t, "validate", fx.StoreDir, base)
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty after validate:\n%s", status)
	}
	if got, want := gitLog(t, fx.StoreRemote, "rev-parse", "refs/heads/main"), gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin main = %s, want the store HEAD %s: the approval was not pushed", got, want)
	}

	// The same brief again: the approval stands, nothing is recorded.
	head := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r = runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.Contains(r.Stdout, "  result: unchanged\n") {
		t.Fatalf("second validate: exit %d, want valid with the approval unchanged\n%s", r.Code, r.Stdout)
	}
	if got := gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != head {
		t.Errorf("second validate committed %s over %s", got, head)
	}

	// No identity: refused before anything is written.
	fx2, env2 := newFixture(t, fixture.Opts{})
	env2 = env2.withoutIdentity().without("EMAIL")
	gitLog(t, fx2.StoreDir, "config", "--unset", "user.name")
	gitLog(t, fx2.StoreDir, "config", "--unset", "user.email")
	gitLog(t, fx2.StoreDir, "config", "user.useConfigOnly", "true")
	head2 := gitLog(t, fx2.StoreDir, "rev-parse", "HEAD")
	r = runJig(t, env2, fx2.StoreDir, "validate", fx2.Ticket)
	if r.Code != 1 || !strings.Contains(r.Stdout, "IDENTITY_REQUIRED") {
		t.Fatalf("validate with no identity: exit %d, want 1 with IDENTITY_REQUIRED\n%s", r.Code, r.Stdout)
	}
	if _, err := os.Stat(filepath.Join(fx2.StoreDir, "tickets", fx2.Ticket, "approval.yaml")); !os.IsNotExist(err) {
		t.Errorf("the refused validate wrote an approval: %v", err)
	}
	if got := gitLog(t, fx2.StoreDir, "rev-parse", "HEAD"); got != head2 {
		t.Errorf("the refused validate committed: HEAD %s, want %s", got, head2)
	}
	if status := gitLog(t, fx2.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the refused validate left the store dirty:\n%s", status)
	}
}
