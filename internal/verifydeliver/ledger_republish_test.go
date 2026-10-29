package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

func readStoreDoc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeStoreDoc(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func answered(id, body, answer string) store.Question {
	return store.Question{ID: id, Status: "answered", Body: body, Answer: answer}
}

// TestRepublishReplacesTheLedgerEntryInPlace: publishing a ticket again puts its
// new entry where its old one was. The file keeps one entry per ticket, in the
// order the tickets were first published, and the entries of the other tickets
// are not touched.
func TestRepublishReplacesTheLedgerEntryInPlace(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	two := []store.Slice{{ID: "a"}, {ID: "b"}}
	for _, ticket := range []string{"JIG-1", "JIG-2", "JIG-3"} {
		if err := writeLedgerEntry(st, ticket, "Title of "+ticket, two, nil); err != nil {
			t.Fatal(err)
		}
	}
	before := readStoreDoc(t, st.LedgerPath())

	questions := []store.Question{answered("q-001", "Which locale?", "English first.")}
	if err := writeLedgerEntry(st, "JIG-2", "Retitled", two[:1], questions); err != nil {
		t.Fatal(err)
	}

	want := strings.Replace(before,
		"## JIG-2 - Title of JIG-2\n\nDelivered 2 slice(s).\n\n**Answers:**\nnone\n",
		"## JIG-2 - Retitled\n\nDelivered 1 slice(s).\n\n**Answers:**\n- q-001: Which locale? -> English first.\n", 1)
	if got := readStoreDoc(t, st.LedgerPath()); got != want {
		t.Errorf("ledger.md after republishing JIG-2 =\n%s\nwant\n%s", got, want)
	}
	entries := recall.ParseLedger(readStoreDoc(t, st.LedgerPath()))
	var tickets []string
	for _, e := range entries {
		tickets = append(tickets, e.Ticket)
	}
	if !reflect.DeepEqual(tickets, []string{"JIG-1", "JIG-2", "JIG-3"}) {
		t.Errorf("entries after the republish = %v, want JIG-1, JIG-2, JIG-3 once each", tickets)
	}
}

// TestRepublishWithTheSameInputsChangesNothing: a second publish that delivers
// what the first did leaves ledger.md and the contract index byte for byte as
// they were, for the first, a middle and the last ticket alike.
func TestRepublishWithTheSameInputsChangesNothing(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	slices := []store.Slice{{ID: "a", Workspace: "alpha"}, {ID: "b", Workspace: "beta"}}
	questions := []store.Question{answered("q-001", "Formal or casual?", "Casual.")}
	tickets := []string{"JIG-1", "JIG-2", "JIG-3"}
	publish := func(ticket string) {
		t.Helper()
		if err := writeLedgerEntry(st, ticket, "Title of "+ticket, slices, questions); err != nil {
			t.Fatal(err)
		}
		if err := writeContractIndexEntry(st, "", ticket, slices, []string{"test", "lint"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ticket := range tickets {
		publish(ticket)
	}
	ledger, index := readStoreDoc(t, st.LedgerPath()), readStoreDoc(t, st.ContractIndexPath(""))

	for _, ticket := range tickets {
		publish(ticket)
		if got := readStoreDoc(t, st.LedgerPath()); got != ledger {
			t.Fatalf("ledger.md after publishing %s again =\n%s\nwant it unchanged:\n%s", ticket, got, ledger)
		}
		if got := readStoreDoc(t, st.ContractIndexPath("")); got != index {
			t.Fatalf("contract-index.md after publishing %s again =\n%s\nwant it unchanged:\n%s", ticket, got, index)
		}
	}
}

// TestRepublishCollapsesTheDuplicatesAnEarlierPublishLeft: a ledger that
// already holds several entries of one ticket (what appending on every publish
// made) is left with one, at the place of the first, when that ticket is
// published again. The entries of the other tickets stay as they were, even
// another ticket's duplicates, which only its own publish replaces.
func TestRepublishCollapsesTheDuplicatesAnEarlierPublishLeft(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	writeStoreDoc(t, st.LedgerPath(), strings.Join([]string{
		"# Ledger",
		"",
		"## JIG-1 - First",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-2 - Old second",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-3 - Third",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-2 - Old second again",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-1 - First again",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
	}, "\n"))

	if err := writeLedgerEntry(st, "JIG-2", "Second", []store.Slice{{ID: "a"}, {ID: "b"}}, nil); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"# Ledger",
		"",
		"## JIG-1 - First",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-2 - Second",
		"",
		"Delivered 2 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-3 - Third",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-1 - First again",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
	}, "\n")
	if got := readStoreDoc(t, st.LedgerPath()); got != want {
		t.Errorf("ledger.md =\n%s\nwant\n%s", got, want)
	}

	// The last entry, repeated at the end of the file, collapses onto the first.
	if err := writeLedgerEntry(st, "JIG-1", "First", []store.Slice{{ID: "a"}}, nil); err != nil {
		t.Fatal(err)
	}
	got := readStoreDoc(t, st.LedgerPath())
	if n := strings.Count(got, "## JIG-1 "); n != 1 {
		t.Errorf("JIG-1 has %d entries after its publish, want 1:\n%s", n, got)
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Errorf("ledger.md ends in a blank line after the last entry was dropped:\n%q", got)
	}
	if !strings.HasPrefix(got, "# Ledger\n\n## JIG-1 - First\n") {
		t.Errorf("JIG-1's entry is not where its first one was:\n%s", got)
	}
}

// TestLedgerEntryLeavesTheRestOfTheFileAlone: what is in ledger.md besides the
// ticket's entry survives a publish - the heading, text between entries, a file
// with no trailing newline. An entry runs to the next "## " heading, as
// recall.ParseLedger reads it, so a deeper heading under it is part of it and
// goes with it when the entry is replaced.
func TestLedgerEntryLeavesTheRestOfTheFileAlone(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	writeStoreDoc(t, st.LedgerPath(), strings.Join([]string{
		"# Ledger",
		"",
		"Notes the person keeps here.",
		"",
		"## JIG-1 - First",
		"",
		"**Answers:**",
		"none",
		"",
		"### A subheading of the entry",
		"",
		"Its text.",
		"",
		"## JIG-1B - Another ticket",
		"",
		"**Answers:**",
		"none",
	}, "\n"))

	if err := writeLedgerEntry(st, "JIG-1", "First", []store.Slice{{ID: "a"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerEntry(st, "JIG-2", "Second", []store.Slice{{ID: "a"}}, nil); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"# Ledger",
		"",
		"Notes the person keeps here.",
		"",
		"## JIG-1 - First",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-1B - Another ticket",
		"",
		"**Answers:**",
		"none",
		"",
		"## JIG-2 - Second",
		"",
		"Delivered 1 slice(s).",
		"",
		"**Answers:**",
		"none",
		"",
	}, "\n")
	if got := readStoreDoc(t, st.LedgerPath()); got != want {
		t.Errorf("ledger.md =\n%s\nwant\n%s", got, want)
	}
}

// TestRepublishReplacesTheContractIndexLineInPlace: the contract index keeps
// one line per ticket, in the order of the first publish, whatever else the
// file holds.
func TestRepublishReplacesTheContractIndexLineInPlace(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	writeStoreDoc(t, st.ContractIndexPath(""), strings.Join([]string{
		"# Contract index",
		"",
		"- JIG-1: workspaces alpha, oracles test",
		"- note: a line of the person's own that names no ticket",
		"- JIG-2: workspaces alpha, oracles test",
		"- JIG-3: workspaces alpha, oracles test",
		"- JIG-2: workspaces alpha, oracles test",
		"",
	}, "\n"))

	slices := []store.Slice{{ID: "a", Workspace: "beta"}, {ID: "b", Workspace: "alpha"}}
	if err := writeContractIndexEntry(st, "", "JIG-2", slices, []string{"test", "lint"}); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"# Contract index",
		"",
		"- JIG-1: workspaces alpha, oracles test",
		"- note: a line of the person's own that names no ticket",
		"- JIG-2: workspaces alpha,beta, oracles lint,test",
		"- JIG-3: workspaces alpha, oracles test",
		"",
	}, "\n")
	if got := readStoreDoc(t, st.ContractIndexPath("")); got != want {
		t.Errorf("contract-index.md =\n%s\nwant\n%s", got, want)
	}
}

// TestContractIndexLivesInTheConfiguredPlatformDirectory: a project that names
// its platform directory gets the index there, and a republish replaces the
// line there.
func TestContractIndexLivesInTheConfiguredPlatformDirectory(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	slices := []store.Slice{{ID: "a", Workspace: "alpha"}}
	for range 2 {
		if err := writeContractIndexEntry(st, "docs/platform/", "JIG-1", slices, []string{"test"}); err != nil {
			t.Fatal(err)
		}
	}
	got := readStoreDoc(t, filepath.Join(st.Root, "docs", "platform", "contract-index.md"))
	if want := "- JIG-1: workspaces alpha, oracles test\n"; got != want {
		t.Errorf("docs/platform/contract-index.md = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(st.Root, "platform")); !os.IsNotExist(err) {
		t.Errorf("the default platform directory exists (stat err %v), want the configured one only", err)
	}
}

// TestPublishAgainKeepsOneEntryInTheLedgerAndTheContractIndex is the same at
// Publish: the second publish of a ticket (TestPublishRepublishesAPublishedTicket)
// leaves ledger.md with one entry of it and the contract index with one line.
func TestPublishAgainKeepsOneEntryInTheLedgerAndTheContractIndex(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	ledger, index := readStoreDoc(t, d.Store.LedgerPath()), readStoreDoc(t, d.Store.ContractIndexPath(""))
	if n := strings.Count(ledger, "## "+fx.Ticket+" - "); n != 1 {
		t.Fatalf("test setup: ledger.md has %d entries of %s after the first publish, want 1:\n%s", n, fx.Ticket, ledger)
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if got := readStoreDoc(t, d.Store.LedgerPath()); got != ledger {
		t.Errorf("ledger.md after the re-publish =\n%s\nwant it as the first publish left it:\n%s", got, ledger)
	}
	if got := readStoreDoc(t, d.Store.ContractIndexPath("")); got != index {
		t.Errorf("contract-index.md after the re-publish =\n%s\nwant it as the first publish left it:\n%s", got, index)
	}
}
