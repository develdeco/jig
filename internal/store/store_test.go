package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// runGit uses gitx.RunRaw, not gitx.Run, because TestPush asserts on the
// exact (untrimmed) text of a `git log --pretty=%s` line, trailing newline
// included.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.RunRaw(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

// newTestRemoteStore creates a bare remote and a working clone with a
// committed project.yaml, and returns a Store rooted at the clone.
func newTestRemoteStore(t *testing.T) (st *Store, work, remote string) {
	t.Helper()
	dir := t.TempDir()
	remote = filepath.Join(dir, "remote.git")
	work = filepath.Join(dir, "work")

	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "clone", remote, work)
	runGit(t, work, "config", "user.name", "tester")
	runGit(t, work, "config", "user.email", "tester@example.invalid")

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "push", "origin", "main")

	st, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	return st, work, remote
}

func TestOpenRequiresProjectYAML(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Fatal("Open: want error for missing project.yaml")
	}
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func TestTicketDir(t *testing.T) {
	st := &Store{Root: filepath.Join("root")}
	want := filepath.Join("root", "JIG-1")
	if got := st.TicketDir("JIG-1"); got != want {
		t.Fatalf("TicketDir = %q, want %q", got, want)
	}
}

func TestSliceStateRoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	got, err := st.ReadSliceState("JIG-1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got != (SliceState{State: "queued"}) {
		t.Fatalf("absent state = %+v, want queued zero-value", got)
	}

	want := SliceState{State: "needs-input", Attempts: 2, Session: "sess-1", Question: "q-001", Reason: "flawed-brief"}
	if err := st.WriteSliceState("JIG-1", "a", want); err != nil {
		t.Fatal(err)
	}
	got, err = st.ReadSliceState("JIG-1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestSlicesAppendRMW(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	empty, err := st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if empty != nil {
		t.Fatalf("ReadSlices on absent file = %v, want nil", empty)
	}

	// Concurrent appenders must not clobber each other: every slice must
	// survive the locked read-modify-write.
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "s" + string(rune('a'+i))
			if err := st.AppendSlices("JIG-1", []Slice{{ID: id, Workspace: "root", Goal: "g", Oracle: "test"}}); err != nil {
				t.Errorf("AppendSlices: %v", err)
			}
		}(i)
	}
	wg.Wait()

	slices, err := st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != n {
		t.Fatalf("len(slices) = %d, want %d (lost update under concurrent append)", len(slices), n)
	}
	seen := map[string]bool{}
	for _, sl := range slices {
		seen[sl.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("distinct slice ids = %d, want %d", len(seen), n)
	}
}

// TestAppendSlicesRejectsDuplicateID checks that appending a slice id that
// already exists in slices.yaml is refused rather than silently duplicating
// the row (which would also reset that slice's attempt counter to 0, per
// the port-fidelity finding on gate's fix-slice append).
func TestAppendSlicesRejectsDuplicateID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	if err := st.AppendSlices("JIG-1", []Slice{{ID: "a", Workspace: "root", Goal: "g", Oracle: "test"}}); err != nil {
		t.Fatalf("first AppendSlices: %v", err)
	}

	err := st.AppendSlices("JIG-1", []Slice{{ID: "a", Workspace: "root", Goal: "g2", Oracle: "test"}})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "SLICE_ID_DUPLICATE" {
		t.Fatalf("err = %v, want *axi.Error SLICE_ID_DUPLICATE", err)
	}

	slices, err := st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != 1 {
		t.Fatalf("len(slices) = %d, want 1 (duplicate must not be appended)", len(slices))
	}
}

// TestSliceStatePathIsSpecForm pins the store schema's on-disk path for slice
// state (see ARCHITECTURE.md): "<ticket>/slices/<id>.state", not the old
// "<ticket>/state/<id>.yaml".
func TestSliceStatePathIsSpecForm(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	if err := st.WriteSliceState("JIG-1", "a", SliceState{State: "green"}); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(st.TicketDir("JIG-1"), "slices", "a.state")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected slice state at %s (store schema form): %v", want, err)
	}

	old := filepath.Join(st.TicketDir("JIG-1"), "state", "a.yaml")
	if _, err := os.Stat(old); err == nil {
		t.Fatalf("slice state was also written at the old path %s", old)
	}
}

func TestQuestionLifecycle(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	if got := st.NextQuestionID("JIG-1"); got != "q-001" {
		t.Fatalf("NextQuestionID (empty) = %q, want q-001", got)
	}

	q := Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}
	if err := st.WriteQuestion("JIG-1", q); err != nil {
		t.Fatal(err)
	}

	if got := st.NextQuestionID("JIG-1"); got != "q-002" {
		t.Fatalf("NextQuestionID (one existing) = %q, want q-002", got)
	}

	qs, err := st.ReadQuestions("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0] != q {
		t.Fatalf("ReadQuestions = %+v, want [%+v]", qs, q)
	}

	slice, err := st.Answer("JIG-1", "q-001", "Casual.")
	if err != nil {
		t.Fatal(err)
	}
	if slice != "c" {
		t.Fatalf("Answer slice = %q, want c", slice)
	}

	qs, err = st.ReadQuestions("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Question{ID: "q-001", Slice: "c", Status: "answered", Body: "Formal or casual greeting?", Answer: "Casual."}
	if len(qs) != 1 || qs[0] != want {
		t.Fatalf("ReadQuestions after answer = %+v, want [%+v]", qs, want)
	}
}

// TestAnswerRejectsNonOpenQuestion asserts Answer refuses to re-answer a
// question that is no longer open, naming the current status, and does not
// clobber the previously recorded answer.
func TestAnswerRejectsNonOpenQuestion(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	q := Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}
	if err := st.WriteQuestion("JIG-1", q); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Answer("JIG-1", "q-001", "Casual."); err != nil {
		t.Fatalf("first Answer: %v", err)
	}

	if _, err := st.Answer("JIG-1", "q-001", "Formal."); err == nil {
		t.Fatal("second Answer: expected an error for an already-answered question, got nil")
	} else if !strings.Contains(err.Error(), "answered") {
		t.Fatalf("second Answer error = %q, want it to mention the current status %q", err.Error(), "answered")
	}

	qs, err := st.ReadQuestions("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0].Answer != "Casual." {
		t.Fatalf("ReadQuestions after rejected re-answer = %+v, want the original answer preserved", qs)
	}
}

// TestSupersedePreservesAnswer asserts that superseding an already-answered
// question keeps the recorded answer text on disk instead of dropping it.
func TestSupersedePreservesAnswer(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	q := Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}
	if err := st.WriteQuestion("JIG-1", q); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Answer("JIG-1", "q-001", "Casual."); err != nil {
		t.Fatal(err)
	}
	if err := st.Supersede("JIG-1", "q-001"); err != nil {
		t.Fatal(err)
	}

	qs, err := st.ReadQuestions("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 {
		t.Fatalf("ReadQuestions = %+v, want 1 question", qs)
	}
	if qs[0].Status != "superseded" {
		t.Fatalf("Status = %q, want superseded", qs[0].Status)
	}
	if qs[0].Answer != "Casual." {
		t.Fatalf("Answer = %q, want %q to survive Supersede", qs[0].Answer, "Casual.")
	}
}

func TestBriefSectionHashes(t *testing.T) {
	brief := "# Brief\n\n## Goal\nDo the thing.\n\n## Slice A - repro\nMake it fail first.   \n\n## Slice B\nThen fix it.\n"
	got := BriefSectionHashes([]byte(brief))

	want := []string{"Goal", "Slice A - repro", "Slice B"}
	for _, h := range want {
		if _, ok := got[h]; !ok {
			t.Fatalf("missing hash for section %q in %v", h, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("BriefSectionHashes returned %d sections, want %d: %v", len(got), len(want), got)
	}

	// Trailing whitespace and CRLF must not change the hash.
	crlf := "# Brief\r\n\r\n## Goal\r\nDo the thing.\r\n"
	if got2 := BriefSectionHashes([]byte(crlf))["Goal"]; got2 != got["Goal"] {
		t.Fatalf("CRLF hash %q != LF hash %q", got2, got["Goal"])
	}

	trailing := "## Goal\nDo the thing.\n\n\n"
	if got3 := BriefSectionHashes([]byte(trailing))["Goal"]; got3 != got["Goal"] {
		t.Fatalf("trailing-whitespace hash %q != base hash %q", got3, got["Goal"])
	}
}

func TestHasRemote(t *testing.T) {
	_, work, _ := newTestRemoteStore(t)
	st, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasRemote() {
		t.Fatal("HasRemote = false, want true")
	}

	standalone := t.TempDir()
	runGit(t, "", "init", "-b", "main", standalone)
	if err := os.WriteFile(filepath.Join(standalone, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stNoRemote, err := Open(standalone)
	if err != nil {
		t.Fatal(err)
	}
	if stNoRemote.HasRemote() {
		t.Fatal("HasRemote = true, want false for a remote-less repo")
	}
}

func TestPush(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("add note"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	log := runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s")
	if got := log; got != "add note\n" {
		t.Fatalf("remote HEAD message = %q, want %q", got, "add note\n")
	}

	// Pushing again with nothing staged must not error and must not create
	// an empty commit.
	beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if err := st.Push("nothing to commit"); err != nil {
		t.Fatalf("Push (no-op): %v", err)
	}
	afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if beforeCount != afterCount {
		t.Fatalf("Push with nothing staged created a commit: %q -> %q", beforeCount, afterCount)
	}
}

func TestSync(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	// Simulate another writer pushing directly to the bare remote.
	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "from-other.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "from other")
	runGit(t, other, "push", "origin", "main")

	if err := st.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "from-other.txt")); err != nil {
		t.Fatalf("Sync did not pull the other writer's commit: %v", err)
	}
}

func TestPushRebasesOnRejection(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	// Diverge the remote out from under the local clone.
	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "from-other.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "from other")
	runGit(t, other, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(work, "mine.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("add mine"); err != nil {
		t.Fatalf("Push should rebase and retry on rejection: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "from-other.txt")); err != nil {
		t.Fatalf("Push did not rebase in the other writer's commit: %v", err)
	}
}
