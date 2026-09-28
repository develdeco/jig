package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newTestChartStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Store{Root: root}
}

func writeChartFile(t *testing.T, st *Store, name, content string) {
	t.Helper()
	dir := filepath.Join(st.Root, "charts", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadChartParsesBlockedByRefs(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - id: T-7
    title: "Slice B"
    blocked_by:
      - ref: "#1"
      - ref: "other-chart#1"
        kind: stacked
      - ref: T-7
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	want := []ChartRef{
		{Ref: "#1"},
		{Ref: "other-chart#1", Kind: "stacked"},
		{Ref: "T-7"},
	}
	if !reflect.DeepEqual(entries[1].BlockedBy, want) {
		t.Fatalf("BlockedBy = %+v, want %+v", entries[1].BlockedBy, want)
	}
}

func TestWriteChartRoundTripsBlockedByRefs(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  # a comment that must survive
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "#1"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart: %v", err)
	}
	entries[0].ID = "T-1"
	entries[1].ID = "T-2"
	entries[1].BlockedBy = []ChartRef{{Ref: "T-1", Kind: "stacked"}}

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "a comment that must survive") {
		t.Fatalf("WriteChart dropped the comment:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	if got[0].ID != "T-1" || got[1].ID != "T-2" {
		t.Fatalf("ids after round trip = %+v", got)
	}
	want := []ChartRef{{Ref: "T-1", Kind: "stacked"}}
	if !reflect.DeepEqual(got[1].BlockedBy, want) {
		t.Fatalf("BlockedBy after round trip = %+v, want %+v", got[1].BlockedBy, want)
	}
}

func TestWriteChartOmitsDefaultKind(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"
	entries[1].BlockedBy = []ChartRef{{Ref: "T-1"}}

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "kind") {
		t.Fatalf("WriteChart wrote a kind for an absent one:\n%s", data)
	}
}

// TestWriteChartKeepsEmptyBlockedByKeyAndComment covers an entry drafted
// with an empty blocked_by (often carrying a head comment like "blockers
// TBD"): the first write-back that gives the entry an id must not drop the
// key or its comment just because there are still no blockers to write.
func TestWriteChartKeepsEmptyBlockedByKeyAndComment(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
    # blockers TBD
    blocked_by:
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "blockers TBD") {
		t.Fatalf("WriteChart dropped the comment on the empty blocked_by key:\n%s", data)
	}
	if !strings.Contains(string(data), "blocked_by") {
		t.Fatalf("WriteChart dropped the empty blocked_by key:\n%s", data)
	}
}

// TestWriteChartKeepsBlockedByItemComments covers a write that only sets an
// entry's id, leaving every entry's blocked_by refs exactly as read (as
// graduate's per-mint write-back does): the comments attached to each
// blocked_by item must survive, not just the key's own head comment.
func TestWriteChartKeepsBlockedByItemComments(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      # A must merge first
      - ref: "#1" # the A entry
        kind: stacked
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "A must merge first") {
		t.Fatalf("WriteChart dropped the blocked_by item's head comment:\n%s", data)
	}
	if !strings.Contains(string(data), "the A entry") {
		t.Fatalf("WriteChart dropped the blocked_by item's line comment:\n%s", data)
	}
}

// TestWriteChartMergesBlockedByItemByItem covers a write whose refs differ
// from what is on disk by one changed element and one appended element: the
// merge must be item by item, not a wholesale rebuild, so a comment or a
// key the store does not model (here "note") survives on every item that
// merely shifts to a new ref, not just on items whose ref is unchanged.
func TestWriteChartMergesBlockedByItemByItem(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      # unchanged item comment
      - ref: "#1" # keep this line comment
        kind: stacked
        note: keep me
      - ref: "#2" # this ref will change
        note: also keep me
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[1].BlockedBy = []ChartRef{
		{Ref: "#1", Kind: "stacked"},
		{Ref: "#3"},
		{Ref: "#4"},
	}

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	refLine := func(ref string) int {
		for i, line := range lines {
			if strings.Contains(line, `ref: "`+ref+`"`) || strings.Contains(line, `ref: '`+ref+`'`) {
				return i
			}
		}
		t.Fatalf("no line with ref %q in written file:\n%s", ref, content)
		return -1
	}

	// The head comment on the sequence, and the item that keeps its ref
	// unchanged, must still precede that item (#1).
	ref1Line := refLine("#1")
	if ref1Line == 0 || !strings.Contains(lines[ref1Line-1], "unchanged item comment") {
		t.Fatalf("head comment did not stay attached to the #1 item:\n%s", content)
	}
	if !strings.Contains(lines[ref1Line], "keep this line comment") {
		t.Fatalf("line comment did not stay attached to the #1 item's ref:\n%s", content)
	}
	// note: keep me must be item #1's own line, not merely present
	// somewhere in the file (it is a substring of "note: also keep me").
	if ref1Line+2 >= len(lines) || strings.TrimSpace(lines[ref1Line+2]) != "note: keep me" {
		t.Fatalf("note: keep me did not stay attached to the #1 item as its own line:\n%s", content)
	}

	// The item whose ref changed from #2 to #3 must keep its own comment
	// and note, attached to the new ref, not to #1 or the appended #4.
	ref3Line := refLine("#3")
	if !strings.Contains(lines[ref3Line], "this ref will change") {
		t.Fatalf("line comment did not follow the #2 item to its new ref #3:\n%s", content)
	}
	if ref3Line+1 >= len(lines) || strings.TrimSpace(lines[ref3Line+1]) != "note: also keep me" {
		t.Fatalf("note: also keep me did not stay attached to the #2 item's new ref #3:\n%s", content)
	}

	// Order on disk: #1, then #3 (formerly #2), then the freshly appended
	// #4 - never #4 or #3 ahead of #1, and never a comment collapsed onto
	// the wrong item.
	ref4Line := refLine("#4")
	if !(ref1Line < ref3Line && ref3Line < ref4Line) {
		t.Fatalf("blocked_by items are out of order after merge (want #1, #3, #4):\n%s", content)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	want := []ChartRef{
		{Ref: "#1", Kind: "stacked"},
		{Ref: "#3"},
		{Ref: "#4"},
	}
	if !reflect.DeepEqual(got[1].BlockedBy, want) {
		t.Fatalf("BlockedBy after merge = %+v, want %+v", got[1].BlockedBy, want)
	}
}

// TestWriteChartFillsEmptyRefKey covers mergeBlockedByItem's setScalarValue
// call on a blocked_by item whose ref key was reserved but left empty: the
// file must still parse afterward, matching id/title/body.
func TestWriteChartFillsEmptyRefKey(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref:
        kind: stacked
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[1].BlockedBy = []ChartRef{{Ref: "#1", Kind: "stacked"}}

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!!null") {
		t.Fatalf("WriteChart left a stale !!null tag on the ref it wrote:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v (the file it just wrote no longer parses)\n%s", err, data)
	}
	want := []ChartRef{{Ref: "#1", Kind: "stacked"}}
	if len(got) != 2 || !reflect.DeepEqual(got[1].BlockedBy, want) {
		t.Fatalf("entry after write = %+v, want BlockedBy %+v", got, want)
	}
}

// TestWriteChartClearsBlockedByWhenEntryHasNone covers a caller that hands
// WriteChart an entry with no BlockedBy for a node that currently lists
// refs: the write must clear them, not leave the old refs in the file while
// the caller believes it wrote none.
func TestWriteChartClearsBlockedByWhenEntryHasNone(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "#1"
      - ref: "#2"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[1].BlockedBy = nil

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "blocked_by") || strings.Contains(string(data), "ref:") {
		t.Fatalf("WriteChart left cleared blockers in the file:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	if len(got[1].BlockedBy) != 0 {
		t.Fatalf("BlockedBy after clearing write = %+v, want none", got[1].BlockedBy)
	}
}

// TestWriteChartDropsBodyKeyWhenCleared covers a caller that hands WriteChart
// an entry with an empty Body for a node that currently has one: the key
// must be removed, matching id, rather than left behind as an empty string.
func TestWriteChartDropsBodyKeyWhenCleared(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
    body: |
      some body text
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Body = ""

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "body:") {
		t.Fatalf("WriteChart left a cleared body key in the file:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Slice A" || got[0].Body != "" {
		t.Fatalf("entry after clearing write = %+v, want title %q and empty body", got, "Slice A")
	}
}

// TestWriteChartKeepsEmptyBodyKeyAndComment is TestWriteChartKeepsEmpty
// BlockedByKeyAndComment's counterpart for body: an entry drafted with an
// empty body (often carrying a head comment like "body TBD") must not have
// the key or its comment dropped just because there is still no body to
// write.
func TestWriteChartKeepsEmptyBodyKeyAndComment(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
    # body TBD
    body:
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "body TBD") {
		t.Fatalf("WriteChart dropped the comment on the empty body key:\n%s", data)
	}
	if !strings.Contains(string(data), "body:") {
		t.Fatalf("WriteChart dropped the empty body key:\n%s", data)
	}
}

// TestWriteChartFillsEmptyBodyKey is TestWriteChartKeepsEmptyBodyKeyAndComment's
// counterpart when the write actually gives the empty body key a value: the
// file must still parse afterward, not come out as an unparseable
// `body: !!null <text>`.
func TestWriteChartFillsEmptyBodyKey(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
    # body TBD
    body:
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Body = "some body text"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!!null") {
		t.Fatalf("WriteChart left a stale !!null tag on the body it wrote:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v (the file it just wrote no longer parses)\n%s", err, data)
	}
	if len(got) != 1 || got[0].Body != "some body text" {
		t.Fatalf("entry after write = %+v, want body \"some body text\"", got)
	}
}

// TestWriteChartInsertsNewIDFirst covers an entry that had no id: the "Add
// missing fields" branch must put the new id key first in the mapping,
// matching the file's documented shape (brief.md:46), not after body and
// blocked_by where it becomes unreadable once the body is multi-line.
func TestWriteChartInsertsNewIDFirst(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice C"
    body: |
      line one
      line two
    blocked_by: []
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-3"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	idIdx := strings.Index(content, "id: T-3")
	titleIdx := strings.Index(content, "title:")
	bodyIdx := strings.Index(content, "body:")
	if idIdx < 0 || titleIdx < 0 || bodyIdx < 0 {
		t.Fatalf("id, title or body missing from written chart:\n%s", content)
	}
	if idIdx > titleIdx || idIdx > bodyIdx {
		t.Fatalf("id was not written before title and body:\n%s", content)
	}
}

// TestWriteChartFillsEmptyIDKey covers a write that gives an id to an entry
// whose id key was reserved but left empty (an `id:` line with no value,
// which decodes as a YAML null): the file must still parse afterward, not
// come out as the unparseable `id: !!null T-1` a stale !!null tag left on
// the node produces once yaml.v3 re-emits it with a non-null Value.
func TestWriteChartFillsEmptyIDKey(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - id:
    title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!!null") {
		t.Fatalf("WriteChart left a stale !!null tag on the id it wrote:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v (the file it just wrote no longer parses)\n%s", err, data)
	}
	if len(got) != 1 || got[0].ID != "T-1" || got[0].Title != "Slice A" {
		t.Fatalf("entry after write = %+v, want id T-1 and title unchanged", got)
	}
}

// TestWriteChartFillsExplicitlyTaggedNullIDKey is TestWriteChartFillsEmptyIDKey's
// counterpart for an id reserved with an explicit `!!null` tag rather than
// left empty: the tag is written by the author, not resolved implicitly by
// yaml.v3 from an empty value, but it must still be cleared before a new
// value is written into it, or yaml.v3 re-emits the unparseable
// `id: !!null T-1`.
func TestWriteChartFillsExplicitlyTaggedNullIDKey(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - id: !!null
    title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!!null") {
		t.Fatalf("WriteChart left a stale !!null tag on the id it wrote:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v (the file it just wrote no longer parses)\n%s", err, data)
	}
	if len(got) != 1 || got[0].ID != "T-1" || got[0].Title != "Slice A" {
		t.Fatalf("entry after write = %+v, want id T-1 and title unchanged", got)
	}
}

// TestWriteChartFillsEmptyTitleKey is TestWriteChartFillsEmptyIDKey's
// counterpart for title: a `title:` line left empty (also a YAML null) that
// a write then gives a value must round-trip the same way.
func TestWriteChartFillsEmptyTitleKey(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title:
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Title = "Slice A"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "!!null") {
		t.Fatalf("WriteChart left a stale !!null tag on the title it wrote:\n%s", data)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v (the file it just wrote no longer parses)\n%s", err, data)
	}
	if len(got) != 1 || got[0].Title != "Slice A" {
		t.Fatalf("entry after write = %+v, want title \"Slice A\"", got)
	}
}

// TestWriteChartStampingIDKeepsOtherScalarsAsWritten covers a write that only
// sets one entry's id: it must not restyle anything else in the file,
// including a sibling entry's quoted title and a blocked_by ref's quotes -
// setScalarValue's blanket Style reset used to strip exactly that, and for
// a quoted "null"-ish title turned it into an actual null. The golden below
// reindents from the fixture's two spaces to four; that is yaml.Marshal's
// own normalization on any write, not part of what this test asserts -
// what matters, and what the byte-for-byte comparison is really pinning, is
// that every scalar's spelling (quotes, the "null" title, the "#1" ref)
// comes back exactly as the author wrote it.
func TestWriteChartStampingIDKeepsOtherScalarsAsWritten(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "null"
    blocked_by:
      - ref: "#1"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Four-space indent here is yaml.Marshal's normalization (the fixture
	// above is two-space), not a claim about indentation this test makes.
	want := "tickets:\n    - id: T-1\n      title: \"Slice A\"\n    - title: \"null\"\n      blocked_by:\n        - ref: \"#1\"\n"
	if string(data) != want {
		t.Fatalf("WriteChart restyled a scalar it had no reason to touch:\ngot:\n%q\nwant:\n%q", data, want)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	if got[1].Title != "null" {
		t.Fatalf("Title = %q, want the quoted string \"null\" preserved, not resolved to an actual null", got[1].Title)
	}
}

// TestWriteChartRefusesWhenEntryCountShrinksUnderLock covers a write
// prepared against a stale read: if the file has fewer entries than the
// write is based on by the time the lock is held, some entry the caller
// means to update was removed since it read the chart unlocked, and
// blindly writing by position would land on the wrong entry. Refuse
// instead.
func TestWriteChartRefusesWhenEntryCountShrinksUnderLock(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"
	entries[1].ID = "T-2"

	// Simulate a concurrent edit that removed an entry after this read.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when the file has fewer entries than this write is based on")
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "T-1") || strings.Contains(string(data), "T-2") {
		t.Fatalf("a refused write still changed the file:\n%s", data)
	}
}

// TestWriteChartPreservesConcurrentlyAppendedEntry covers a human (or
// another jig) appending a new entry to the chart file between this
// caller's unlocked ReadChart and its locked WriteChart: the appended entry
// must survive, not be truncated away.
func TestWriteChartPreservesConcurrentlyAppendedEntry(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	// Simulate a concurrent append after this read.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatalf("ReadChart after write: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries after write = %+v, want the concurrently appended \"Slice B\" to survive", got)
	}
	if got[0].ID != "T-1" {
		t.Fatalf("entries[0].ID = %q, want T-1", got[0].ID)
	}
	if got[1].Title != "Slice B" {
		t.Fatalf("entries[1] = %+v, want the concurrently appended entry untouched", got[1])
	}
}

// TestWriteChartRefusesWhenEntryIsInsertedUnderLock covers a concurrent edit
// that inserts a new entry in the middle of the file: the count check alone
// tolerates this (the file still has at least len(entries) entries), but
// updating by position would land entry 2's id on what is now entry 3.
func TestWriteChartRefusesWhenEntryIsInsertedUnderLock(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"
	entries[1].ID = "T-2"

	// Simulate a concurrent edit that inserted a new entry between A and B.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice X"
  - title: "Slice B"
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when an entry was inserted under the write's positions")
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got[2].ID != "" {
		t.Fatalf("a refused write still stamped entry 3 (really Slice B): %+v", got[2])
	}
}

// TestWriteChartRefusesWhenEntriesAreReorderedUnderLock covers a concurrent
// edit that swaps two entries: the count is unchanged, but the entry now at
// each position is not the one this write is based on.
func TestWriteChartRefusesWhenEntriesAreReorderedUnderLock(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"
	entries[1].ID = "T-2"

	// Simulate a concurrent edit that swapped A and B.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice B"
  - title: "Slice A"
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when entries were reordered under the write's positions")
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "" || got[1].ID != "" {
		t.Fatalf("a refused write still stamped a reordered entry: %+v", got)
	}
}

// TestWriteChartRefusesWhenTitleEditedConcurrently covers a human fixing a
// typo in an entry's title while a write prepared against the old title is
// in flight: the write must refuse rather than silently revert the typo fix,
// even though this call never means to touch that entry's title at all.
func TestWriteChartRefusesWhenTitleEditedConcurrently(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slcie B"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	// Simulate a human fixing the typo in entry 2's title while this write
	// (which only means to stamp entry 1's id) is in flight.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when a title changed concurrently at a position this write is based on")
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Slice B") {
		t.Fatalf("WriteChart reverted the concurrent typo fix:\n%s", data)
	}
	if strings.Contains(string(data), "T-1") {
		t.Fatalf("a refused write still stamped entry 1's id:\n%s", data)
	}
}

// TestWriteChartRefusesWhenBodyEditedConcurrently is TestWriteChart
// RefusesWhenTitleEditedConcurrently's counterpart for body, the other field
// updateChartEntryInNode writes unconditionally from the caller's snapshot.
func TestWriteChartRefusesWhenBodyEditedConcurrently(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    body: |
      old body
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	// Simulate a human editing entry 2's body while this write (which only
	// means to stamp entry 1's id) is in flight.
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    body: |
      new body
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when a body changed concurrently at a position this write is based on")
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new body") {
		t.Fatalf("WriteChart reverted the concurrent body edit:\n%s", data)
	}
	if strings.Contains(string(data), "T-1") {
		t.Fatalf("a refused write still stamped entry 1's id:\n%s", data)
	}
}

// TestWriteChartRefusesWhenIDRecordedConcurrently covers a concurrent-write
// race: this run ReadChart's an entry with no id, a concurrent
// run records an id for that same position (title and body untouched, so
// the existing guard misses it), and this run then tries to write its own
// id over the top. Without an id comparison this silently orphans the
// concurrently minted ticket.
func TestWriteChartRefusesWhenIDRecordedConcurrently(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	// Simulate a concurrent run minting and recording an id for this entry.
	writeChartFile(t, st, "demo", `tickets:
  - id: T-9
    title: "Slice A"
`)

	if err := st.WriteChart("demo", entries); err == nil {
		t.Fatalf("WriteChart: want an error when an id was recorded concurrently at a position this write is based on")
	}

	data, err := os.ReadFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "T-9") {
		t.Fatalf("WriteChart clobbered the concurrently recorded id:\n%s", data)
	}
	if strings.Contains(string(data), "T-1") {
		t.Fatalf("a refused write still stamped its own id:\n%s", data)
	}
}

// TestWriteChartAllowsRepeatedWriteOfSameID covers graduate's own per-mint
// write-back pattern: a later write in the same run re-reads the chart (or
// simply retries) and writes the same id it already recorded. The file's id
// already equals entry.ID even though it no longer equals what was read, and
// that must not be refused.
func TestWriteChartAllowsRepeatedWriteOfSameID(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].ID = "T-1"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart (first mint): %v", err)
	}

	// entries[0].asRead still reflects the pre-mint read (id ""), but the
	// file's id ("T-1") now matches entries[0].ID, not a concurrent run.
	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart (repeated write of the same id): %v", err)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "T-1" {
		t.Fatalf("entries[0].ID after repeated write = %q, want T-1", got[0].ID)
	}
}

// TestWriteChartAllowsIntentionalTitleChangeWhenFileUnchanged covers the
// other side of the same guard: a caller that reads an entry and then means
// to change its own title (or body) must still be able to, as long as
// nothing else touched the file first. The guard compares the file against
// what was read, not against what the caller now wants to write.
func TestWriteChartAllowsIntentionalTitleChangeWhenFileUnchanged(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Title = "Slice A, renamed"

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Slice A, renamed" {
		t.Fatalf("entries[0].Title = %q, want the caller's intentional rename to stick", got[0].Title)
	}
}

// TestWriteChartOverwritesConcurrentlyIntroducedMapping covers the one path
// that reaches setScalarValue's non-scalar conversion: a concurrent edit
// turns a scalar field into a mapping between the caller's ReadChart and its
// WriteChart. Unlike TestWriteChartRefusesWhenTitleEditedConcurrently, where
// the field stays a scalar and the stale-read guard's struct decode catches
// the change, decoding a mapping into that same struct's string field fails,
// so the guard (charts.go, the existingNode.Decode(&current) check) skips
// this entry instead of refusing the write. The write then proceeds and
// overwrites the concurrently introduced mapping with the caller's title.
func TestWriteChartOverwritesConcurrentlyIntroducedMapping(t *testing.T) {
	st := newTestChartStore(t)
	writeChartFile(t, st, "demo", `tickets:
  - title: "Slice A"
`)

	entries, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Title = "Slice A, renamed"

	// Simulate a concurrent edit that turns entry 1's title into a mapping
	// while this write is in flight.
	writeChartFile(t, st, "demo", `tickets:
  - title: {oops: true}
`)

	if err := st.WriteChart("demo", entries); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}

	got, err := st.ReadChart("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "Slice A, renamed" {
		t.Fatalf("entries[0].Title = %q, want the write to have overwritten the concurrently introduced mapping", got[0].Title)
	}
}
