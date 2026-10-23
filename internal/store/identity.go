package store

import (
	"sync"

	"github.com/develdeco/jig/internal/gitx"
)

// identityCache holds the identity Store.identity resolved, once it has
// resolved one. Its mutex guards both fields: a frontier run shares one Store
// across its per-slice goroutines.
type identityCache struct {
	mu sync.Mutex
	id gitx.Identity
	ok bool
}

// identity returns the person's git identity in the store clone: the name and
// email every store commit is authored and committed with, so the store's
// history names the person who ran jig and never an identity jig made up.
// It resolves the identity on first use (gitx.ResolveIdentity, which honors
// the environment, the clone's own config and the global config) and keeps it
// for this Store's life. A clone with none fails with IDENTITY_REQUIRED, and
// the failure is not kept, so the next call asks git again.
//
// Every path asks for it only once it has something to commit: the in-process
// commit (gitx.Repo.CommitAllAs) takes identity as its lookup, since it
// cannot read git config itself, and the git program's commits are given it
// as -c user.name and -c user.email. Both author a commit the same way, and
// the environment still wins over both, as it does for git. The lookup costs
// a process, so a command that commits nothing, which is most of them, pays
// nothing for it.
func (s *Store) identity() (gitx.Identity, error) {
	s.ident.mu.Lock()
	defer s.ident.mu.Unlock()
	if s.ident.ok {
		return s.ident.id, nil
	}
	id, err := gitx.ResolveIdentity(s.Root)
	if err != nil {
		return gitx.Identity{}, err
	}
	s.ident.id, s.ident.ok = id, true
	return id, nil
}

// commitConfig is the git program's -c arguments that author a commit as the
// person: it prepends them to the commit command's own arguments.
func commitConfig(id gitx.Identity, args ...string) []string {
	return append([]string{"-c", "user.name=" + id.Name, "-c", "user.email=" + id.Email}, args...)
}
