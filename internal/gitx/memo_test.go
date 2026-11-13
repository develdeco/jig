package gitx

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// memoRepo makes a repository whose origin is url, writing its config by
// hand so the test starts one git process, not several.
func memoRepo(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	setMemoOrigin(t, dir, url)
	return dir
}

// setMemoOrigin rewrites dir's config with origin at url, as another process
// editing the file would.
func setMemoOrigin(t *testing.T, dir, url string) {
	t.Helper()
	cfg := "[core]\n\trepositoryformatversion = 0\n[user]\n\tname = memo\n\temail = memo@example.invalid\n[remote \"origin\"]\n\turl = " + url + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func mustOrigin(t *testing.T, m *Memo, dir, want string) {
	t.Helper()
	got, err := m.OriginURL(dir, "origin")
	if err != nil || got != want {
		t.Fatalf("OriginURL = %q, %v; want %q", got, err, want)
	}
}

// TestMemoAsksEachQuestionOnce: a second ask of any of the four questions
// costs no git question, and the answer is the first one.
func TestMemoAsksEachQuestionOnce(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	m := NewMemo()
	for i := 0; i < 3; i++ {
		mustOrigin(t, m, dir, "https://example.invalid/a.git")
	}
	if got := m.Asked(); got != 1 {
		t.Fatalf("Asked after three OriginURL = %d, want 1", got)
	}
	top1, err := m.TopLevel(dir)
	if err != nil {
		t.Fatal(err)
	}
	top2, _ := m.TopLevel(dir)
	common1, err := m.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	common2, _ := m.CommonDir(dir)
	id1, err := m.Identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := m.Identity(dir)
	if top1 != top2 || common1 != common2 || id1 != id2 {
		t.Errorf("a hit answered differently: %q/%q %q/%q %v/%v", top1, top2, common1, common2, id1, id2)
	}
	want, _ := TopLevel(dir)
	if top1 != want {
		t.Errorf("TopLevel = %q, want git's %q", top1, want)
	}
	if got := m.Asked(); got != 4 {
		t.Errorf("Asked = %d, want 4 (one per question)", got)
	}
}

// TestMemoKeepsANoSuchRemoteAnswer: a remote git does not have is an answer
// too, asked once.
func TestMemoKeepsANoSuchRemoteAnswer(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	m := NewMemo()
	for i := 0; i < 2; i++ {
		if _, err := m.OriginURL(dir, "upstream"); err == nil {
			t.Fatal("OriginURL(upstream) succeeded, want git's no such remote")
		}
	}
	if got := m.Asked(); got != 1 {
		t.Errorf("Asked = %d, want 1", got)
	}
}

// TestMemoForgetMakesTheNextAskRunGit: Forget drops a repository's entries,
// and only that repository's.
func TestMemoForgetMakesTheNextAskRunGit(t *testing.T) {
	a := memoRepo(t, "https://example.invalid/a.git")
	b := memoRepo(t, "https://example.invalid/b.git")
	m := NewMemo()
	mustOrigin(t, m, a, "https://example.invalid/a.git")
	mustOrigin(t, m, b, "https://example.invalid/b.git")
	m.Forget(a)
	mustOrigin(t, m, a, "https://example.invalid/a.git")
	mustOrigin(t, m, b, "https://example.invalid/b.git")
	if got := m.Asked(); got != 3 {
		t.Errorf("Asked = %d, want 3 (a, b, then a again)", got)
	}
	if _, err := m.TopLevel(a); err != nil {
		t.Fatal(err)
	}
	m.Forget(a)
	if _, err := m.TopLevel(a); err != nil {
		t.Fatal(err)
	}
	if got := m.Asked(); got != 5 {
		t.Errorf("Asked after a forgotten TopLevel = %d, want 5", got)
	}
}

// TestMemoSeesAConfigFileAnotherProcessChanged: a remote changed behind the
// memo's back, with no Forget, is seen on the next ask.
func TestMemoSeesAConfigFileAnotherProcessChanged(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	m := NewMemo()
	mustOrigin(t, m, dir, "https://example.invalid/a.git")
	setMemoOrigin(t, dir, "https://example.invalid/much-longer/b.git")
	mustOrigin(t, m, dir, "https://example.invalid/much-longer/b.git")
	if got := m.Asked(); got != 2 {
		t.Errorf("Asked = %d, want 2", got)
	}
	if _, err := m.Identity(dir); err != nil {
		t.Fatal(err)
	}
	setMemoOrigin(t, dir, "https://example.invalid/b.git")
	if _, err := m.Identity(dir); err != nil {
		t.Fatal(err)
	}
	if got := m.Asked(); got != 4 {
		t.Errorf("Asked after a changed config and an identity ask = %d, want 4", got)
	}
}

// TestNilMemoAsksGitEveryTime: no memo is today's behavior.
func TestNilMemoAsksGitEveryTime(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	var m *Memo
	mustOrigin(t, m, dir, "https://example.invalid/a.git")
	setMemoOrigin(t, dir, "https://example.invalid/b.git")
	mustOrigin(t, m, dir, "https://example.invalid/b.git")
	if _, err := m.TopLevel(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CommonDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Identity(dir); err != nil {
		t.Fatal(err)
	}
	m.Forget(dir)
	if got := m.Asked(); got != 0 {
		t.Errorf("nil Asked = %d, want 0", got)
	}
}

// TestMemosNeverShareAnEntry: each command's memo asks for itself.
func TestMemosNeverShareAnEntry(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	one, two := NewMemo(), NewMemo()
	mustOrigin(t, one, dir, "https://example.invalid/a.git")
	mustOrigin(t, two, dir, "https://example.invalid/a.git")
	if one.Asked() != 1 || two.Asked() != 1 {
		t.Errorf("Asked = %d and %d, want 1 and 1", one.Asked(), two.Asked())
	}
}

// TestMemoIsSafeForConcurrentUse: callers racing for one answer ask git once.
func TestMemoIsSafeForConcurrentUse(t *testing.T) {
	dir := memoRepo(t, "https://example.invalid/a.git")
	m := NewMemo()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := m.OriginURL(dir, "origin"); err != nil || got != "https://example.invalid/a.git" {
				t.Errorf("OriginURL = %q, %v", got, err)
			}
			if _, err := m.TopLevel(dir); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := m.Asked(); got != 2 {
		t.Errorf("Asked = %d, want 2", got)
	}
}
