package migrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// putStoreFile writes rel (slash-separated) under the store and commits it, so
// the store stays as clean as the migration's refusals need.
func putStoreFile(t *testing.T, st *store.Store, rel, text string) {
	t.Helper()
	path := filepath.Join(st.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, st.Root, "add", "-A")
	runGit(t, st.Root, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "add "+rel)
}

func readStoreFile(t *testing.T, st *store.Store, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(st.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A ledger as publish wrote it before a republish replaced entries: T-2
// published twice, a ticket the store no longer has (T-9), and ids in the
// titles and answers that are the person's words, not headings.
const v1Ledger = `# Ledger

## T-1 - Mint ids - not T-2

Delivered 2 slice(s).

**Answers:**
- q-001: Keep T-1 around? -> Yes, as an alias of T-1.

## T-2 - Graph

Delivered 1 slice(s).

**Answers:**
none

## T-9 - A ticket the store no longer has

Delivered 1 slice(s).

**Answers:**
none

## T-2 - Graph again

Delivered 3 slice(s).

**Answers:**
none
`

const v1ContractIndex = `# Contract index

- T-1: workspaces alpha, oracles test
- T-2: workspaces beta, oracles lint,test
- T-9: workspaces gamma, oracles test
- see T-1: a line of the person's own
- T-2: workspaces beta, oracles lint,test
`

func renameMapTwoTickets() Map {
	return Map{
		Keys:    map[string]string{"STORE": "the store", "GRAPH": "the graph"},
		Tickets: map[string]string{"T-1": "STORE", "T-2": "GRAPH"},
	}
}

// TestApplyRenamesTheTicketsInTheLedgerAndTheContractIndex: after the
// migration the ledger's entry headings and the contract index's lines name
// the ids the tickets have now, in the one commit, and nothing else in either
// file moved.
func TestApplyRenamesTheTicketsInTheLedgerAndTheContractIndex(t *testing.T) {
	t.Parallel()
	st := newV1GitStore(t)
	putStoreFile(t, st, "ledger.md", v1Ledger)
	putStoreFile(t, st, "platform/contract-index.md", v1ContractIndex)

	plan, err := BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Apply(st, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantLedger := strings.NewReplacer(
		"## T-1 - Mint ids", "## STORE-1 - Mint ids",
		"## T-2 - Graph", "## GRAPH-1 - Graph",
	).Replace(v1Ledger)
	if got := readStoreFile(t, st, "ledger.md"); got != wantLedger {
		t.Errorf("ledger.md =\n%s\nwant\n%s", got, wantLedger)
	}
	wantIndex := strings.NewReplacer(
		"- T-1: workspaces", "- STORE-1: workspaces",
		"- T-2: workspaces", "- GRAPH-1: workspaces",
	).Replace(v1ContractIndex)
	if got := readStoreFile(t, st, "platform/contract-index.md"); got != wantIndex {
		t.Errorf("platform/contract-index.md =\n%s\nwant\n%s", got, wantIndex)
	}

	var tickets []string
	for _, e := range recall.ParseLedger(readStoreFile(t, st, "ledger.md")) {
		tickets = append(tickets, e.Ticket)
	}
	if want := []string{"STORE-1", "GRAPH-1", "T-9", "GRAPH-1"}; !reflect.DeepEqual(tickets, want) {
		t.Errorf("jig recall's reading of the ledger names %v, want %v", tickets, want)
	}

	if status := runGit(t, st.Root, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain = %q, want a clean tree after the migration's commit", status)
	}
	changed := runGit(t, st.Root, "diff", "--name-only", "HEAD~1", "HEAD")
	for _, rel := range []string{"ledger.md", "platform/contract-index.md"} {
		if !strings.Contains(changed, rel) {
			t.Errorf("the migration commit changed\n%s\nwant %s among them", changed, rel)
		}
	}
}

// TestRewritingTheLedgerAndContractIndexTwiceChangesNothingTheSecondTime: a
// store whose ids were already renamed through the same map is not renamed
// again, and not rewritten.
func TestRewritingTheLedgerAndContractIndexTwiceChangesNothingTheSecondTime(t *testing.T) {
	t.Parallel()
	st := newV1GitStore(t)
	putStoreFile(t, st, "ledger.md", v1Ledger)
	putStoreFile(t, st, "platform/contract-index.md", v1ContractIndex)
	plan, err := BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Apply(st, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ledger, index := readStoreFile(t, st, "ledger.md"), readStoreFile(t, st, "platform/contract-index.md")

	if err := rewriteStoreDocs(st, plan); err != nil {
		t.Fatalf("rewriteStoreDocs again: %v", err)
	}
	if got := readStoreFile(t, st, "ledger.md"); got != ledger {
		t.Errorf("ledger.md changed on the second rewrite:\n%s\nwas\n%s", got, ledger)
	}
	if got := readStoreFile(t, st, "platform/contract-index.md"); got != index {
		t.Errorf("platform/contract-index.md changed on the second rewrite:\n%s\nwas\n%s", got, index)
	}
	if status := runGit(t, st.Root, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain = %q after the second rewrite, want a clean tree", status)
	}
}

// TestRewriteKeepsTheLineEndingsAndTheMissingFinalNewline: only the id bytes
// of a heading or a line change, so a file with CRLF line endings, or none at
// its end, comes out with the ones it had.
func TestRewriteKeepsTheLineEndingsAndTheMissingFinalNewline(t *testing.T) {
	t.Parallel()
	st := newV1GitStore(t)
	putStoreFile(t, st, "ledger.md", "# Ledger\r\n\r\n## T-1 - First\r\n\r\n**Answers:**\r\nnone\r\n\r\n## T-2\r\n")
	putStoreFile(t, st, "platform/contract-index.md", "- T-1: workspaces a, oracles test\r\n- T-2: workspaces a, oracles test")
	plan, err := BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if err := Apply(st, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got, want := readStoreFile(t, st, "ledger.md"), "# Ledger\r\n\r\n## STORE-1 - First\r\n\r\n**Answers:**\r\nnone\r\n\r\n## GRAPH-1\r\n"; got != want {
		t.Errorf("ledger.md = %q, want %q", got, want)
	}
	if got, want := readStoreFile(t, st, "platform/contract-index.md"), "- STORE-1: workspaces a, oracles test\r\n- GRAPH-1: workspaces a, oracles test"; got != want {
		t.Errorf("contract-index.md = %q, want %q", got, want)
	}
}

// TestBuildPlanListsTheLedgerAndContractIndexItWouldRewrite: the dry run says
// which of the two files the renames reach, and writes neither. A file that
// is not there, or names no renamed ticket, is left out.
func TestBuildPlanListsTheLedgerAndContractIndexItWouldRewrite(t *testing.T) {
	t.Parallel()
	rewrites := func(plan Plan) []string {
		var paths []string
		for _, f := range plan.Files {
			if f.Path == "ledger.md" || strings.HasSuffix(f.Path, "contract-index.md") {
				paths = append(paths, f.Action+" "+f.Path)
			}
		}
		return paths
	}

	st := newV1GitStore(t)
	plan, err := BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := rewrites(plan); len(got) != 0 {
		t.Errorf("a store with neither file plans %v, want nothing", got)
	}

	putStoreFile(t, st, "ledger.md", "# Ledger\n\n## T-9 - Not renamed\n")
	putStoreFile(t, st, "platform/contract-index.md", v1ContractIndex)
	plan, err = BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got, want := rewrites(plan), []string{"rewrite platform/contract-index.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("plan lists %v, want %v: the ledger names no renamed ticket", got, want)
	}

	putStoreFile(t, st, "ledger.md", v1Ledger)
	plan, err = BuildPlan(st, renameMapTwoTickets(), "")
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got, want := rewrites(plan), []string{"rewrite ledger.md", "rewrite platform/contract-index.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("plan lists %v, want %v", got, want)
	}
	if got := readStoreFile(t, st, "ledger.md"); got != v1Ledger {
		t.Errorf("the dry run wrote ledger.md:\n%s", got)
	}
}

// TestStepOneRenamesTheContractIndexInTheConfiguredPlatformDirectory: the
// migration step reads the project's platform directory from its config, so
// the contract index in notes/platform/ is renamed and the default
// platform/ is not looked in.
func TestStepOneRenamesTheContractIndexInTheConfiguredPlatformDirectory(t *testing.T) {
	t.Parallel()
	st := newV1GitStore(t)
	putStoreFile(t, st, "notes/platform/contract-index.md", v1ContractIndex)
	mapPath := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(mapPath, []byte("keys:\n  STORE: the store\n  GRAPH: the graph\ntickets:\n  T-1: STORE\n  T-2: GRAPH\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Store: st, Cfg: project.Config{SchemaVersion: 1, Platform: "notes/platform"}, MapPath: mapPath}

	plan, err := Steps[1].Plan(in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var listed bool
	for _, f := range plan.Files {
		listed = listed || (f.Action == "rewrite" && f.Path == "notes/platform/contract-index.md")
	}
	if !listed {
		t.Errorf("plan files = %+v, want a rewrite of notes/platform/contract-index.md", plan.Files)
	}
	if err := Steps[1].Apply(in, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readStoreFile(t, st, "notes/platform/contract-index.md"); !strings.HasPrefix(got, "# Contract index\n\n- STORE-1: workspaces alpha") {
		t.Errorf("notes/platform/contract-index.md =\n%s\nwant it renamed", got)
	}
}
