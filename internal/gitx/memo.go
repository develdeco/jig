package gitx

import "sync/atomic"

// Memo is a command's memory of git's answers; see the tests for the contract.
type Memo struct{ asked atomic.Int64 }

// NewMemo returns an empty memo for one command.
func NewMemo() *Memo { return &Memo{} }

// OriginURL returns the URL of dir's remote.
func (m *Memo) OriginURL(dir, remote string) (string, error) {
	m.count()
	return Run(dir, "remote", "get-url", remote)
}

// TopLevel returns dir's worktree top level.
func (m *Memo) TopLevel(dir string) (string, error) { m.count(); return TopLevel(dir) }

// CommonDir returns dir's common git directory.
func (m *Memo) CommonDir(dir string) (string, error) { m.count(); return CommonDir(dir) }

// Identity returns the author identity git resolves for dir.
func (m *Memo) Identity(dir string) (Identity, error) { m.count(); return ResolveIdentity(dir) }

// Forget drops what the memo holds for dir.
func (m *Memo) Forget(dir string) {}

// Asked is how many questions the memo sent to git.
func (m *Memo) Asked() int {
	if m == nil {
		return 0
	}
	return int(m.asked.Load())
}

func (m *Memo) count() {
	if m != nil {
		m.asked.Add(1)
	}
}
