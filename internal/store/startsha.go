package store

import "path/filepath"

// StartSHAPath returns <ticket>/start.<repo>.sha, the file that records a
// ticket's start sha in one repo: the sha its branch started from, which a
// build's commits must descend from (frontier's verifyGreen) and which a
// gate falls back to as a scope base. It is the one place that names the
// file.
func (s *Store) StartSHAPath(ticket, repo string) string {
	return filepath.Join(s.TicketDir(ticket), "start."+repo+".sha")
}

// WriteStartSHA records sha as ticket's start sha in repo, replacing any
// earlier one. Frontier writes it the first time it dispatches into a repo
// (origin's target, or the branch's own tip when origin already has it);
// adoption writes it as the adopted branch's tip and replaces whatever a
// dispatch that built nothing left.
func (s *Store) WriteStartSHA(ticket, repo, sha string) error {
	return AtomicWrite(s.StartSHAPath(ticket, repo), []byte(sha))
}
