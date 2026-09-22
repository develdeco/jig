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
	"gopkg.in/yaml.v3"
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

	want := SliceState{State: "needs-input", Attempts: 2, Session: "sess-1", Question: "q-001", Reason: "flawed-brief", Signature: "a|code-bug|nil pointer"}
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

// TestSliceStateSignatureNoKeyWhenAbsent checks that a slice state written
// with no Signature re-marshals with no "signature:" key at all (an older
// jig's on-disk states must stay byte-unchanged by this additive field).
func TestSliceStateSignatureNoKeyWhenAbsent(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	if err := st.WriteSliceState("JIG-1", "a", SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(st.stateFile("JIG-1", "a"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["signature"]; ok {
		t.Fatalf("state with no Signature wrote a signature: key: %v", raw)
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

// TestSyncCommitsUncommittedLeftoversBeforeRebase reproduces the store wedge
// left by a command that failed after writing to the store (a dirty
// tracked file plus an untracked attempt work file) at the same time as a
// divergent remote commit: Sync must stage and commit the leftovers with
// jig's identity, then still pull the other writer's commit, instead of
// failing "cannot pull with rebase: you have unstaged changes".
func TestSyncCommitsUncommittedLeftoversBeforeRebase(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	// Leave dirty + untracked leftovers, as a command that failed after
	// writing to the store would (a modified journal.ndjson-like file and
	// an untracked attempt work file).
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "JIG-1", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "JIG-1", "work", "a.attempt-1.result.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate another writer pushing directly to the bare remote, so Sync's
	// pull --rebase has real work to do.
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

	if status := runGit(t, work, "status", "--porcelain"); status != "" {
		t.Fatalf("Sync left the tree dirty: %q", status)
	}
	if _, err := os.Stat(filepath.Join(work, "from-other.txt")); err != nil {
		t.Fatalf("Sync did not pull the other writer's commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "JIG-1", "work", "a.attempt-1.result.json")); err != nil {
		t.Fatalf("Sync lost the leftover work file: %v", err)
	}

	log := runGit(t, work, "log", "--oneline", "--grep=jig: record uncommitted store state")
	if strings.TrimSpace(log) == "" {
		t.Fatal("Sync did not commit the leftovers with the expected message")
	}
	author := runGit(t, work, "log", "-1", "--grep=jig: record uncommitted store state", "--pretty=%an <%ae>")
	if strings.TrimSpace(author) != "jig <jig@invalid>" {
		t.Fatalf("leftover commit author = %q, want jig <jig@invalid>", strings.TrimSpace(author))
	}
}

// TestPushLeavesNoMidRebaseOnConflict reproduces a real conflicting write
// from a second clone: Push's own retry `pull --rebase` conflicts (both
// clones change the same line), so Push must fail, but jig itself must
// never leave the store mid-rebase - the next command's Sync needs a clean
// tree to detect, not a repo already wedged by this failed Push.
func TestPushLeavesNoMidRebaseOnConflict(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "commit", "-am", "remote change")
	runGit(t, other, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := st.Push("local change")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Push with a conflicting retry pull: err = %v, want *axi.Error STORE_CONFLICT", err)
	}

	mid, ierr := inProgressRebaseOrMerge(work)
	if ierr != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", ierr)
	}
	if mid {
		t.Fatal("Push left the store mid-rebase after a failed retry pull")
	}
}

// TestSyncRefusesWhileMidRebase places the store mid-rebase by hand (a real
// conflict, not a simulated one) and checks that Sync refuses with
// STORE_CONFLICT, commits nothing, and leaves the rebase state untouched for
// the operator to resolve.
func TestSyncRefusesWhileMidRebase(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "local change")

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "commit", "-am", "remote change")
	runGit(t, other, "push", "origin", "main")

	runGit(t, work, "fetch", "origin")
	if _, err := gitx.Run(work, "rebase", "origin/main"); err == nil {
		t.Fatal("expected the rebase to conflict")
	}
	mid, err := inProgressRebaseOrMerge(work)
	if err != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", err)
	}
	if !mid {
		t.Fatal("fixture did not leave the repo mid-rebase; test setup is wrong")
	}

	beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")

	err = st.Sync()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Sync mid-rebase: err = %v, want *axi.Error STORE_CONFLICT", err)
	}

	afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if beforeCount != afterCount {
		t.Fatalf("Sync committed while mid-rebase: HEAD count %q -> %q", beforeCount, afterCount)
	}
	midAfter, err := inProgressRebaseOrMerge(work)
	if err != nil {
		t.Fatalf("inProgressRebaseOrMerge after Sync: %v", err)
	}
	if !midAfter {
		t.Fatal("Sync must not touch the rebase state; only the operator resolves it")
	}
}

// TestPushRefusesWhileMidMerge checks that Push (as `jig requeue`, which
// never Syncs first) refuses with STORE_CONFLICT when the store was left
// mid-merge by hand (a real conflicting `git merge`), instead of staging
// and committing the unresolved conflict markers as a merge commit and
// pushing them to the shared store remote.
func TestPushRefusesWhileMidMerge(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "local change")

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "commit", "-am", "remote change")
	runGit(t, other, "push", "origin", "main")

	runGit(t, work, "fetch", "origin")
	if _, err := gitx.Run(work, "merge", "origin/main"); err == nil {
		t.Fatal("expected the merge to conflict")
	}
	mid, err := inProgressRebaseOrMerge(work)
	if err != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", err)
	}
	if !mid {
		t.Fatal("fixture did not leave the repo mid-merge; test setup is wrong")
	}

	beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")
	remoteBefore := runGit(t, "", "--git-dir", remote, "rev-parse", "main")

	err = st.Push("requeue from brief diff")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Push mid-merge: err = %v, want *axi.Error STORE_CONFLICT", err)
	}

	afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if beforeCount != afterCount {
		t.Fatalf("Push committed while mid-merge: HEAD count %q -> %q", beforeCount, afterCount)
	}
	remoteAfter := runGit(t, "", "--git-dir", remote, "rev-parse", "main")
	if remoteBefore != remoteAfter {
		t.Fatal("Push pushed while mid-merge: remote main moved")
	}
}

// TestPushRefusesWhileMidRebase checks that Push refuses with STORE_CONFLICT
// when the store is already mid-rebase (left by hand, or by the operator's
// own conflicting `git pull --rebase`), instead of running its own
// `add -A`/commit over the unresolved conflict, and leaves the rebase state
// untouched afterward for the operator to resolve.
func TestPushRefusesWhileMidRebase(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "local change")

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "commit", "-am", "remote change")
	runGit(t, other, "push", "origin", "main")

	runGit(t, work, "fetch", "origin")
	if _, err := gitx.Run(work, "rebase", "origin/main"); err == nil {
		t.Fatal("expected the rebase to conflict")
	}
	mid, err := inProgressRebaseOrMerge(work)
	if err != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", err)
	}
	if !mid {
		t.Fatal("fixture did not leave the repo mid-rebase; test setup is wrong")
	}

	err = st.Push("requeue from brief diff")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Push mid-rebase: err = %v, want *axi.Error STORE_CONFLICT", err)
	}

	midAfter, err := inProgressRebaseOrMerge(work)
	if err != nil {
		t.Fatalf("inProgressRebaseOrMerge after Push: %v", err)
	}
	if !midAfter {
		t.Fatal("Push must not touch the rebase state; only the operator resolves it")
	}
}

// TestSyncOwnConflictingPullAbortsAndWraps checks that when Sync's own
// pull --rebase conflicts (not a pre-existing mid-rebase state, but a
// conflict Sync's own retry causes), Sync still aborts it - dropping that
// abort leaves the store mid-rebase for the next command to trip over - and
// returns STORE_CONFLICT instead of git's raw, by-then-stale pull error. The
// message must name the conflicting path and the pre-pull commit the store
// landed back on, built from state jig itself read, and must never carry
// git's own stderr hint text ("git rebase --continue"/"--skip"/"--abort"),
// which fails once jig's own abort has already run.
func TestSyncOwnConflictingPullAbortsAndWraps(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "commit", "-am", "remote change")
	runGit(t, other, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := st.Sync()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Sync with its own conflicting pull: err = %v, want *axi.Error STORE_CONFLICT", err)
	}
	if !strings.Contains(ae.Msg, "project.yaml") {
		t.Fatalf("Sync conflict Msg = %q, want the conflicting path named", ae.Msg)
	}
	// The abort resets the branch to the commit Sync's own stageAndCommit
	// made just before the pull (its "record uncommitted store state"
	// commit, not the fixture's original init commit), which is where HEAD
	// now sits.
	backAt := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if !strings.Contains(ae.Msg, backAt) {
		t.Fatalf("Sync conflict Msg = %q, want the pre-pull commit %s (HEAD after the abort) named", ae.Msg, backAt)
	}
	full := ae.Msg + " " + strings.Join(ae.Help, " ")
	if strings.Contains(full, "hint:") || strings.Contains(full, "--skip") || strings.Contains(full, "--continue`") {
		t.Fatalf("Sync conflict message carries git's raw stderr hint text: %q", full)
	}

	mid, ierr := inProgressRebaseOrMerge(work)
	if ierr != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", ierr)
	}
	if mid {
		t.Fatal("Sync left the store mid-rebase after its own conflicting pull")
	}
}

// leaveConflictedStashPop leaves work's project.yaml with unmerged index
// entries from a conflicted `git stash pop`, without setting MERGE_HEAD or
// either rebase directory: refuseIfMidRebaseOrMerge must catch this from
// the unmerged index alone, not from any of the three marker files.
func leaveConflictedStashPop(t *testing.T, work string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: stashed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(work, "stash", "push", "-m", "mystash"); err != nil {
		t.Fatalf("stash push: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "local change")
	if _, err := gitx.Run(work, "stash", "pop"); err == nil {
		t.Fatal("expected the stash pop to conflict; test setup is wrong")
	}
}

// leaveConflictedCherryPick leaves work's project.yaml with unmerged index
// entries from a conflicted `git cherry-pick`, which sets CHERRY_PICK_HEAD,
// not MERGE_HEAD: refuseIfMidRebaseOrMerge must not rely on that marker.
func leaveConflictedCherryPick(t *testing.T, work string) {
	t.Helper()
	runGit(t, work, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "other change")
	otherSHA := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	runGit(t, work, "checkout", "main")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "main change")
	if _, err := gitx.Run(work, "cherry-pick", otherSHA); err == nil {
		t.Fatal("expected the cherry-pick to conflict; test setup is wrong")
	}
}

// leaveResolvedCherryPick leaves work with a conflicting `git cherry-pick`
// already resolved and staged (no unmerged index entries) but not concluded
// with `--continue`: CHERRY_PICK_HEAD is still set. Without CHERRY_PICK_HEAD
// in inProgressRebaseOrMerge's marker list, and with the index already
// clean, nothing catches this state: Sync would `add -A` and commit the
// user's resolved pick as "jig: record uncommitted store state" under the
// original author, and Push would publish it.
func leaveResolvedCherryPick(t *testing.T, work string) {
	t.Helper()
	leaveConflictedCherryPick(t, work)
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "project.yaml")
	if unmerged := runGit(t, work, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(unmerged) != "" {
		t.Fatalf("fixture setup left unmerged paths after resolving: %q", unmerged)
	}
}

// leaveResolvedRevert leaves work with a conflicting `git revert` already
// resolved and staged (no unmerged index entries) but not concluded with
// `--continue`: REVERT_HEAD is still set, and neither of the other two
// pre-existing markers (rebase-merge, rebase-apply, MERGE_HEAD) is.
func leaveResolvedRevert(t *testing.T, work string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "add x=a")
	addSHA := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "change x=b")
	if _, err := gitx.Run(work, "revert", "--no-edit", addSHA); err == nil {
		t.Fatal("expected the revert to conflict; test setup is wrong")
	}
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "project.yaml")
	if unmerged := runGit(t, work, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(unmerged) != "" {
		t.Fatalf("fixture setup left unmerged paths after resolving: %q", unmerged)
	}
}

// leaveRebaseApplyInProgress leaves work mid-rebase under the "apply"
// backend, which uses .git/rebase-apply rather than .git/rebase-merge, with
// the conflict already resolved and staged (no unmerged index entries):
// dropping "rebase-apply" from inProgressRebaseOrMerge's marker list must
// not survive this test, and the unmerged-index check must not accidentally
// cover for its absence, since the index here is already clean.
func leaveRebaseApplyInProgress(t *testing.T, work string) {
	t.Helper()
	runGit(t, work, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "other change")
	runGit(t, work, "checkout", "main")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-am", "main change")
	if _, err := gitx.Run(work, "-c", "rebase.backend=apply", "rebase", "other"); err == nil {
		t.Fatal("expected the rebase to conflict; test setup is wrong")
	}
	// Resolve and stage the conflict, but do not run `rebase --continue`:
	// the index is now clean, so only the rebase-apply marker still shows
	// the rebase is unfinished.
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nx: resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "project.yaml")
	if unmerged := runGit(t, work, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(unmerged) != "" {
		t.Fatalf("fixture setup left unmerged paths after resolving: %q", unmerged)
	}
}

// assertRefusesStoreConflict runs Sync or Push against st (already left in
// a broken state by one of the leaveConflicted* helpers above) and checks
// it refuses with STORE_CONFLICT, commits nothing, pushes nothing, and
// leaves the broken state exactly as it found it for the operator to
// resolve.
func assertRefusesStoreConflict(t *testing.T, st *Store, work, remote, which string) {
	t.Helper()
	beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")
	remoteBefore := runGit(t, "", "--git-dir", remote, "rev-parse", "main")

	var err error
	switch which {
	case "Sync":
		err = st.Sync()
	case "Push":
		err = st.Push("should be refused")
	default:
		t.Fatalf("unknown case %q", which)
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("%s with unresolved conflicts in the store: err = %v, want *axi.Error STORE_CONFLICT", which, err)
	}

	afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if beforeCount != afterCount {
		t.Fatalf("%s committed over unresolved conflicts: HEAD count %q -> %q", which, beforeCount, afterCount)
	}
	remoteAfter := runGit(t, "", "--git-dir", remote, "rev-parse", "main")
	if remoteBefore != remoteAfter {
		t.Fatalf("%s pushed while the store had unresolved conflicts: remote main moved", which)
	}
	stillBroken, ierr := inProgressRebaseOrMerge(work)
	if ierr != nil {
		t.Fatalf("inProgressRebaseOrMerge: %v", ierr)
	}
	if !stillBroken {
		t.Fatalf("%s: the fixture's broken state is gone after the call, so this run proved nothing", which)
	}
}

// TestSyncAndPushRefuseAfterConflictedStashPop: a conflicted `git stash
// pop` leaves unmerged index entries with none of
// refuseIfMidRebaseOrMerge's three marker files. Without the
// unmerged-index check, Sync and Push would `add -A` the conflict markers
// and commit (Push would then push them to the shared remote).
func TestSyncAndPushRefuseAfterConflictedStashPop(t *testing.T) {
	for _, which := range []string{"Sync", "Push"} {
		t.Run(which, func(t *testing.T) {
			st, work, remote := newTestRemoteStore(t)
			leaveConflictedStashPop(t, work)
			assertRefusesStoreConflict(t, st, work, remote, which)
		})
	}
}

// TestSyncAndPushRefuseAfterConflictedCherryPick: a conflicted
// `git cherry-pick` sets CHERRY_PICK_HEAD, not MERGE_HEAD, so the guard
// must key off the unmerged index, not a fixed list of marker files.
func TestSyncAndPushRefuseAfterConflictedCherryPick(t *testing.T) {
	for _, which := range []string{"Sync", "Push"} {
		t.Run(which, func(t *testing.T) {
			st, work, remote := newTestRemoteStore(t)
			leaveConflictedCherryPick(t, work)
			assertRefusesStoreConflict(t, st, work, remote, which)
		})
	}
}

// TestSyncAndPushRefuseAfterResolvedCherryPickOrRevert: a conflicting
// cherry-pick or revert that the user has resolved and staged (no unmerged
// index entries left) still has CHERRY_PICK_HEAD or REVERT_HEAD set until
// `--continue` or `--abort` runs. Before those two joined
// inProgressRebaseOrMerge's marker list, this state slipped past both the
// marker check and the (by-then-clean) unmerged-index check, so Sync would
// commit the user's resolved pick or revert as "jig: record uncommitted
// store state" under jig's own identity and Push would publish it.
func TestSyncAndPushRefuseAfterResolvedCherryPickOrRevert(t *testing.T) {
	fixtures := map[string]func(*testing.T, string){
		"CherryPick": leaveResolvedCherryPick,
		"Revert":     leaveResolvedRevert,
	}
	for name, fixture := range fixtures {
		for _, which := range []string{"Sync", "Push"} {
			t.Run(name+"/"+which, func(t *testing.T) {
				st, work, remote := newTestRemoteStore(t)
				fixture(t, work)
				assertRefusesStoreConflict(t, st, work, remote, which)
			})
		}
	}
}

// TestSyncAndPushRefuseWhileRebaseApplyInProgress: the "apply" rebase
// backend leaves .git/rebase-apply rather than .git/rebase-merge. Dropping
// "rebase-apply" from inProgressRebaseOrMerge's marker list would survive
// every other test in this file, since only this fixture leaves the index
// clean while still mid-rebase; this one catches that case specifically.
func TestSyncAndPushRefuseWhileRebaseApplyInProgress(t *testing.T) {
	for _, which := range []string{"Sync", "Push"} {
		t.Run(which, func(t *testing.T) {
			st, work, remote := newTestRemoteStore(t)
			leaveRebaseApplyInProgress(t, work)
			assertRefusesStoreConflict(t, st, work, remote, which)
		})
	}
}

// TestSyncAndPushRefuseDetachedHEAD: mid-`git bisect` (or any other detached
// checkout) HEAD does not point at a branch. `git rev-parse --abbrev-ref
// HEAD` happily prints the literal "HEAD" for that, which would send Sync
// and Push into a `pull`/`push origin HEAD`; currentBranch instead uses
// `git symbolic-ref`, which fails on a detached HEAD, so both refuse with
// STORE_CONFLICT before touching anything.
func TestSyncAndPushRefuseDetachedHEAD(t *testing.T) {
	for _, which := range []string{"Sync", "Push"} {
		t.Run(which, func(t *testing.T) {
			st, work, remote := newTestRemoteStore(t)
			sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
			runGit(t, work, "checkout", sha)
			if err := os.WriteFile(filepath.Join(work, "uncommitted.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")
			remoteBefore := runGit(t, "", "--git-dir", remote, "rev-parse", "main")

			var err error
			switch which {
			case "Sync":
				err = st.Sync()
			case "Push":
				err = st.Push("should be refused")
			}
			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
				t.Fatalf("%s with a detached HEAD: err = %v, want *axi.Error STORE_CONFLICT", which, err)
			}

			afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
			if beforeCount != afterCount {
				t.Fatalf("%s committed on a detached HEAD: HEAD count %q -> %q", which, beforeCount, afterCount)
			}
			remoteAfter := runGit(t, "", "--git-dir", remote, "rev-parse", "main")
			if remoteBefore != remoteAfter {
				t.Fatalf("%s pushed from a detached HEAD: remote main moved", which)
			}
		})
	}
}

// newTestStandaloneStore creates a non-bare, remote-less git repo with a
// committed project.yaml (what `jig init --standalone` produces) and
// returns a Store rooted there.
func newTestStandaloneStore(t *testing.T) (st *Store, work string) {
	t.Helper()
	work = t.TempDir()
	runGit(t, "", "init", "-b", "main", work)
	runGit(t, work, "config", "user.name", "tester")
	runGit(t, work, "config", "user.email", "tester@example.invalid")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "init")
	st, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	return st, work
}

// TestSyncRefusesStandaloneStoreMidConflict: Sync's unfinished-rebase-or-
// merge refusal must run before its no-remote early return, so a standalone
// store (`init --standalone`, no origin) left with an unresolved conflict is
// still refused at the start of the command, not silently let through
// because there is nothing to pull or push.
func TestSyncRefusesStandaloneStoreMidConflict(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	if st.HasRemote() {
		t.Fatal("fixture has a remote; test setup is wrong")
	}
	leaveConflictedCherryPick(t, work)

	beforeCount := runGit(t, work, "rev-list", "--count", "HEAD")
	err := st.Sync()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Sync on a standalone store mid-conflict: err = %v, want *axi.Error STORE_CONFLICT", err)
	}
	afterCount := runGit(t, work, "rev-list", "--count", "HEAD")
	if beforeCount != afterCount {
		t.Fatalf("Sync committed on a standalone store mid-conflict: HEAD count %q -> %q", beforeCount, afterCount)
	}
}

// TestWrapAbortedPullConflict: the message must not claim the rebase "was
// aborted" when the best-effort `rebase --abort` itself failed, and the
// Help line must name the real branch, never a literal "<branch>"
// placeholder. When the rebase state could not even be read before that
// abort failed, the message must not assert the store is mid-rebase either
// (that was never confirmed) and must instead say the state is unknown and
// point at `git status` in the store. The successful-abort case must name
// the conflicting paths and the pre-pull commit it landed back on, and must
// never carry git's own stderr hint text ("rebase --continue"/"--skip"),
// which fails once jig's own abort has already run.
func TestWrapAbortedPullConflict(t *testing.T) {
	st := &Store{Root: "/store"}
	gitHint := `hint: Resolve all conflicts, then run "git rebase --continue".` +
		`hint: Or skip: "git rebase --skip". hint: Or abort: "git rebase --abort".`

	ok := st.wrapAbortedPullConflict(nil, nil, []string{"internal/store/store.go"}, "abc123", "main")
	var aeOK *axi.Error
	if !errors.As(ok, &aeOK) || aeOK.Code != "STORE_CONFLICT" {
		t.Fatalf("abort ok: err = %v, want *axi.Error STORE_CONFLICT", ok)
	}
	if !strings.Contains(aeOK.Msg, "was aborted") {
		t.Fatalf("abort ok: Msg = %q, want it to say the rebase was aborted", aeOK.Msg)
	}
	if !strings.Contains(aeOK.Msg, "internal/store/store.go") {
		t.Fatalf("abort ok: Msg = %q, want the conflicting path named", aeOK.Msg)
	}
	if !strings.Contains(aeOK.Msg, "abc123") {
		t.Fatalf("abort ok: Msg = %q, want the pre-pull commit sha named", aeOK.Msg)
	}
	full := aeOK.Msg + " " + strings.Join(aeOK.Help, " ")
	if strings.Contains(full, "hint:") || strings.Contains(full, gitHint) {
		t.Fatalf("abort ok: message carries git's raw stderr hint text: %q", full)
	}
	if len(aeOK.Help) == 0 || !strings.Contains(aeOK.Help[0], "origin main") || strings.Contains(aeOK.Help[0], "<branch>") {
		t.Fatalf("abort ok: Help = %v, want the real branch name (not a <branch> placeholder)", aeOK.Help)
	}

	failed := st.wrapAbortedPullConflict(errors.New("could not detach HEAD"), nil, nil, "", "main")
	var aeFailed *axi.Error
	if !errors.As(failed, &aeFailed) || aeFailed.Code != "STORE_CONFLICT" {
		t.Fatalf("abort failed: err = %v, want *axi.Error STORE_CONFLICT", failed)
	}
	if strings.Contains(aeFailed.Msg, "was aborted") {
		t.Fatalf("abort failed: Msg = %q, want it not to claim the rebase was aborted when the abort itself failed", aeFailed.Msg)
	}
	if !strings.Contains(aeFailed.Msg, "could not detach HEAD") {
		t.Fatalf("abort failed: Msg = %q, want the abort's own error included", aeFailed.Msg)
	}
	if len(aeFailed.Help) == 0 || !strings.Contains(aeFailed.Help[0], "mid-rebase") {
		t.Fatalf("abort failed: Help = %v, want it to point at the store still being mid-rebase", aeFailed.Help)
	}

	// The rebase-state read itself failed (for example the git-path lookup
	// errored), and the abort then failed too: the message must not claim
	// the store is mid-rebase, since that was never actually confirmed - it
	// must say the state is unknown instead, and it must not assert the
	// pull "conflicted" either, since that too was never confirmed.
	unknown := st.wrapAbortedPullConflict(errors.New("no rebase in progress"), errors.New("could not read git state"), nil, "", "main")
	var aeUnknown *axi.Error
	if !errors.As(unknown, &aeUnknown) || aeUnknown.Code != "STORE_CONFLICT" {
		t.Fatalf("state unknown: err = %v, want *axi.Error STORE_CONFLICT", unknown)
	}
	if strings.Contains(aeUnknown.Msg, "was aborted") {
		t.Fatalf("state unknown: Msg = %q, want it not to claim the rebase was aborted", aeUnknown.Msg)
	}
	if strings.Contains(aeUnknown.Msg, "conflicted") {
		t.Fatalf("state unknown: Msg = %q, want it not to claim a conflict that was never confirmed", aeUnknown.Msg)
	}
	if !strings.Contains(aeUnknown.Msg, "could not read git state") {
		t.Fatalf("state unknown: Msg = %q, want the state read's own error included", aeUnknown.Msg)
	}
	if strings.Contains(strings.Join(aeUnknown.Help, " "), "mid-rebase") {
		t.Fatalf("state unknown: Help = %v, want it not to assert the store is mid-rebase when that was never confirmed", aeUnknown.Help)
	}
	if len(aeUnknown.Help) == 0 || !strings.Contains(aeUnknown.Help[0], "git status") {
		t.Fatalf("state unknown: Help = %v, want it to point at `git status` in the store", aeUnknown.Help)
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

// TestUnreachableRemoteIsNotReportedAsConflict checks that a pull which
// fails before rebasing (here: the remote path no longer exists) returns
// git's own error from both Sync and Push, not STORE_CONFLICT with
// conflict-resolution help, and leaves nothing to abort.
func TestUnreachableRemoteIsNotReportedAsConflict(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	runGit(t, work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "moved.git"))

	for name, run := range map[string]func() error{
		"Sync": st.Sync,
		"Push": func() error { return st.Push("after the remote moved") },
	} {
		err := run()
		if err == nil {
			t.Fatalf("%s with an unreachable remote: err = nil, want git's error", name)
		}
		var ae *axi.Error
		if errors.As(err, &ae) && ae.Code == "STORE_CONFLICT" {
			t.Fatalf("%s with an unreachable remote was misreported as STORE_CONFLICT: %v", name, err)
		}
		mid, ierr := inProgressRebaseOrMerge(work)
		if ierr != nil {
			t.Fatalf("inProgressRebaseOrMerge: %v", ierr)
		}
		if mid {
			t.Fatalf("%s left the store mid-rebase", name)
		}
	}
}
