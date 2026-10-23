package main

// recordBranch makes the branch the work is on the ticket's branch when that
// branch names no ticket: a session that started on a branch of its own (the
// harness's worktree branch, a name the person chose) is not on the ticket's
// <id>-<slug> default, nothing about the name says whose it is, and the claim
// is where the session says it is this ticket's. Recording it (ticket.yaml
// branch:) is what lets publish, the gate and `jig next` name the branch the
// work is really on.
//
// Nothing is recorded when
//   - the work is on no branch (a detached HEAD, a directory outside the repo);
//   - the ticket already records a branch (it has one, adopted or recorded by
//     an earlier claim, and a claim never replaces it);
//   - the branch names a ticket: this one, whose default or old name it is,
//     and binds as it stands, or another, whose branch it is not ours to take;
//   - the name cannot be a ticket's branch (the repo's target branch, which a
//     push must not land on, or a name git rejects).
//
// It returns the branch it recorded, "" when it recorded none.
func recordBranch(w work) (string, error) {
	branch := w.Where.Branch
	if branch == "" {
		return "", nil
	}
	rec, err := w.St.ReadTicket(w.Ticket)
	if err != nil {
		return "", err
	}
	if rec.Adopted() {
		return "", nil
	}
	if id, _, err := w.St.TicketForBranch(branch); err != nil || id != "" {
		return "", nil
	}
	if w.St.CheckAdoptableBranch(w.Ticket, branch, targetBranch(w)) != nil {
		return "", nil
	}
	if err := w.St.WriteTicketBranch(w.Ticket, branch); err != nil {
		return "", err
	}
	return branch, nil
}

// targetBranch is the branch the work's repo lands on: the repo's declared
// target, main when the project declares none or the work is in no repo of it.
func targetBranch(w work) string {
	for _, r := range w.Cfg.Repos {
		if r.Name() == w.Where.Repo {
			return r.TargetBranch()
		}
	}
	return "main"
}
