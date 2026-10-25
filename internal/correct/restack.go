package correct

import (
	"fmt"

	"github.com/develdeco/jig/internal/stack"
)

// RestackRef fingerprints the restack of the ticket's branch in repo r onto
// its parent's head: one for each head the parent's branch moves to.
func RestackRef(r stack.Repo) string {
	return "restack:" + r.Parent + ":" + r.Repo + ":" + short12(r.ParentHead)
}

// RetargetRef fingerprints the retarget a parent's merge in repo r asks for:
// one for each parent and repo, since a parent merges once.
func RetargetRef(r stack.Repo) string {
	return "retarget:" + r.Parent + ":" + r.Repo
}

// restackFacts finds what the stack of the ticket asks of its branch: in each
// repo it shares with a parent whose branch moved past it, a restack onto the
// parent's head (a merge the session makes; jig never rebases); in each repo
// whose parent merged while the ticket's branch lacks the target's head, a
// retarget of its pull request to the target and a merge of it. A ticket
// stacked on nothing, or one Input.Stack does not read, has none.
func restackFacts(in Input) ([]fact, error) {
	if in.Stack == nil {
		return nil, nil
	}
	plan, err := in.Stack()
	if err != nil {
		return nil, err
	}
	var out []fact
	for _, r := range plan.Repos {
		switch {
		case r.Behind:
			out = append(out, fact{note: Note{Ref: RestackRef(r), Kind: KindRestack, Text: restackText(in.Ticket, r)}, commit: r.ParentHead})
		case r.Retarget:
			out = append(out, fact{note: Note{Ref: RetargetRef(r), Kind: KindRetarget, Text: retargetText(in.Ticket, r)}})
		}
	}
	return out, nil
}

// restackText is what the session is told when the parent's branch moved past
// the ticket's.
func restackText(ticket string, r stack.Repo) string {
	return fmt.Sprintf("%s is stacked on %s, whose branch moved past it: restack onto `%s` at `%s`: `git merge %s` in %s. jig never rebases: the merge is yours to make.",
		ticket, r.Parent, r.ParentBranch, short12(r.ParentHead), r.ParentRef, r.Repo)
}

// retargetText is what the session is told when the parent merged.
func retargetText(ticket string, r stack.Repo) string {
	return fmt.Sprintf("%s is stacked on %s, and the parent merged in %s: retarget the PR to %s and merge %s (`git merge origin/%s`).",
		ticket, r.Parent, r.Repo, r.Target, r.Target, r.Target)
}
