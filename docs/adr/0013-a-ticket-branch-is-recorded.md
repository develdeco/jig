# A ticket's branch is recorded, not derived

A ticket used to have one branch, `jig/<ticket>`, and every command derived
it from the ticket's id: the build cut it from the target, the gate fetched
it from the build lease, publish squashed and pushed it. That fits work jig
builds from a brief. It does not fit a branch someone else built. A
developer who already has `add-retry` pushed - no brief, no slices, only
code - could point `jig gate --branch` at it for one review, and no more:
the fix slices that review queued were built on `jig/<ticket>`, cut from
the target, which lacks the branch's code; the ticket's start sha was the
target's tip, so a commit on the branch's own history could never descend
from it; and the next `jig gate` went back to reviewing `jig/<ticket>`, a
branch that never held the author's work. The loop a branch built outside
jig needs - review it, build the fixes on it, review it again - had nowhere
to stand.

## The branch is recorded

A ticket's working branch is a fact about the ticket, so it lives on the
ticket, in `<ticket>/ticket.yaml`'s `branch:` (ADR 0003, ADR 0010: the store
schema is the API, and a fact has one owner). `store.TicketBranch` resolves
it, defaulting to `jig/<ticket>` when none is recorded, and every command
that names the ticket's branch resolves it there, once per command.

The first `jig gate <ticket> --branch <name>` adopts the branch: it records
`branch:` (creating `ticket.yaml` when the ticket has none, keeping the
title and blockers it has) and the ticket's start sha. From then on the
branch is the ticket's, and `--branch` is no longer a per-round mode: naming
the recorded branch again changes nothing, naming another is refused
(`BRANCH_MISMATCH`). A ticket that adopted a branch is an adopted ticket,
and needs no brief and no slices to be gated, built and solved; the gate
round queues its fixes as slices like any other.

An adoption is refused, with an axi code and help, when it cannot stand:

- the name is the target branch, or one git rejects or expands to another
  name (`TICKET_BRANCH_INVALID`, the check `TicketBranch` applies to a
  recorded branch, applied before recording it);
- the branch is not on origin (`BRANCH_NOT_FOUND`);
- jig already built on the ticket's own branch (`TICKET_ALREADY_BUILT`):
  adopting another branch would strand the work. The judge is the store, never
  this machine's lease: a `verified` line in the journal, a green `result` line
  naming a commit, or a slice in state `green` (below). A lease shows what one
  machine has and the store what any machine did, and a ticket built on one
  machine must not be adoptable from another. A ticket a jig that predates the
  `verified` lines built (v0.1.x) has green `result` lines and no such line,
  and keeps them after a requeue has set its slices back to queued, so it is
  refused just the same. What the journal records as built is below.

Every refusal, this one included, leaves nothing behind. The adoption is
written last of a round's preconditions and first of its effects, just
before its journal line, so a plain rerun is the whole recovery.

An adopted branch is on origin by definition, and stays a fact the ticket can
rely on: a gate round or a build that finds it gone refuses with
`BRANCH_NOT_FOUND` (`pool.MustExistOnOrigin`) instead of `pool.Acquire`
cutting it from the target, which would review or build on a branch that lacks
the author's code.

## The commits jig built

The frontier journals a `verified` line when a green result's commit passes
`verifyGreen`, after the `result` line that records what the builder claimed.
Those lines, and only those, are the commits jig built on the ticket's branch
(`journal.BuiltCommits`). A claim that did not verify - a sha that is no commit
in the lease, the start sha itself, a commit off the branch - put nothing on the
branch, and treating it as built would refuse the ticket for good over a commit
that never existed. Everything below that needs the commits asks this one
question: the start sha, the gate's choice of copy, the build, and the
push-first hint.

Adoption asks the coarser question, whether jig built at all, and reads it from
three facts, any of which says so. The `verified` line is the first. A green
`result` line naming a commit is the second: every jig version journals one
before it routes the result, and nothing removes it. A journal from a jig that
predates the `verified` lines (v0.1.x) has it and no other trace of a build
once `jig requeue --from-brief-diff` has set its green slices back to queued,
and reading the `verified` lines and the slices alone would let a ticket
carried across an upgrade adopt a branch, for good, and drop its own green work
out of every later round. A claim is not proof, so this fact decides only where
there is no `verified` line to say better. A ticket of this version whose green
claims all failed verification is then refused as well: a loud refusal it
recovers from by minting a ticket for the branch, where adopting it would be
silent and permanent. A slice in state `green` is the third, which every jig
version writes only after the slice's commit verified. The commit list the
adopted path needs is the `verified` lines' alone, and an adopted ticket has
them, since only this version adopts.

## The start sha follows the branch until jig builds on it

A commit jig builds is accepted only if it descends from the ticket's start
sha (`verifyGreen`), and a gate round's scope falls back to it when the branch
has no merge base with the target. Squash and reconcile do not read it. For a
branch jig cut from the target the start sha is the target's tip. For an
adopted branch it is the branch's own tip, whatever the target has done since:
the target may have moved past the branch's fork point, and a start sha taken
from it would reject every fix built on the author's history. Adoption writes
it, replacing one that an earlier dispatch, which built nothing, left.

The author owns the branch until jig builds on it, so its tip moves. While the
journal records no commits jig built, every dispatch records origin's tip again
(`ensureStartSHA`), where the lease was just cut or fast-forwarded to: the
fixes are built on the branch as it is when jig starts, an author who pushed to
it or rewrote it after the adoption included. A start sha fixed at adoption
would sit on the old history of a rewritten branch, every commit a build made
would fail verification, and the slices would stall with no stated cause. Once
jig has built, the start sha stays: those commits descend from it, so a lease
that holds them holds it. The rule is for an adopted branch jig has built
nothing on, and for nothing else: an ordinary ticket's start sha never follows
origin. Its slices are committed on `jig/<ticket>`, cut from the target as it
was, so a start sha that followed a target which moved between two runs would
disown every commit of the earlier ones.

The first dispatch into a repo records the start sha where the lease's branch
started (`origin/<branch>` when origin has the branch, else `origin/<target>`,
`pool.Acquire`'s own rule), so a branch recorded any other way is not measured
against the target either.

## A lease is synced with its branch on origin

`pool.Acquire` kept an existing local branch as it was, so a lease of a
branch that someone else keeps pushing to fell behind and stayed there. The
rule is general, not a case of adoption: when the branch exists on origin,
acquiring a lease (build, gate or publish) fetches, with `--prune` so that a
branch deleted on origin is gone from the lease's view of it too, and compares
(`pool.Compare`).

- the lease has no commits of its own: it is fast-forwarded to origin's tip;
- the lease is ahead of origin: its commits are jig's own, not yet pushed,
  and are kept;
- each has commits the other lacks: `BRANCH_DIVERGED`, and the lease is left
  as it is. jig merges, rebases and resets nothing that is not its own, and
  does not say whose the commits are: the author may have pushed while jig
  built on the branch, or rewritten its history, and jig itself pushes a
  squash of a ticket's commits when it publishes. The one exception is a build
  of an adopted branch whose lease holds no commit the journal records jig
  built that origin lacks (`pool.RecutUnlessBuilt`; the rule,
  `pool.HoldsUnpushedBuilt`, is the gate's too, below): the branch is still
  origin's to follow, what the lease holds of its own is the history a rewrite
  replaced, an attempt's leftovers, or leftovers on top of jig's commits that
  origin already has, and the lease is re-cut from origin's tip. An author who
  rewrote the branch after a first run that parked a question, before jig built
  anything, is followed instead of sent to merge the discarded history back in.
  The re-cut, like the fast-forward, stops with git's own error over an
  uncommitted edit it would overwrite, and discards none. A lease with a
  leftover but no divergence keeps it, and the retry builds on it.

A branch origin does not have is left as it is. The ordinary `jig/<ticket>`
lives only in the build lease until a publish pushes it, so until then there
is nothing to compare. After a publish that squashed it, it is on origin as the
squash, and the build lease keeps the unsquashed commits it was built from: the
two stand diverged, and a build acquire after a publish - a `jig run` after a
post-publish requeue - stops with `BRANCH_DIVERGED`, and so does a gate round or
a publish, which judge the same two copies (below). Without the rule the build
went on and failed later, at a second publish's push. That state is left as it is
(below): the refusal names the way on, merging origin's branch into the build
lease, and a publish from there pushes as it is.

A lease that is re-pointed right after it is acquired keeps no commits of its
own that anyone needs. The gate lease is re-pointed at the round's source, so
the gate drops its own local copy of the branch before acquiring: a copy left
over from an earlier round must not refuse the acquire over commits nobody
keeps. The publish lease is re-pointed the same way, from the copy the gate
reviewed, and drops its copy the same way: a copy left by an earlier attempt -
a declined publish, one that failed after it squashed - stands diverged from
the branch once it has reached origin another way, and would refuse the
acquire before it is replaced.

## What a round reviews, and what a build builds on

The gate's copy of the ticket's branch is the one that holds the commits jig
built on it. On an adopted branch, while the journal records none, that is
origin's: the branch is the author's. Once it records some they stay in the
build lease until someone pushes them, so which copy holds them depends on where
they are now. The round judges it by the pool's own sync rule, applied to the
build lease's copy and origin's (`chooseBuiltCopy`). The rule is the branch's,
not the kind of ticket's: the ticket's own `jig/<ticket>` is judged the same way
as soon as origin has a copy of it, which a publish gives it.

- origin has no copy of the branch: the build lease's is the only one, and there
  is nothing to compare. That is the ticket's own `jig/<ticket>` until a publish
  pushes it; an adopted branch is on origin by definition;
- the build lease's copy is in step with origin's, or behind it: origin holds
  everything the lease does, jig's commits included - a publish pushed them, or
  the human did - and whatever was added since, by the author or by a publish
  (the merge of the target, the memorize commit). The round reviews origin's
  copy. The lease's older copy is not the branch's head, and a clean round over
  it would say nothing of the newer commits;
- it is ahead: jig's commits are not on origin yet, and origin has nothing the
  lease lacks. The round reviews the lease's copy;
- the two have diverged, and the build lease's copy holds a commit jig built
  that origin lacks: neither copy holds both that commit and what origin has that
  the lease lacks - the author's commit, or the squash a first publish pushed in
  place of the commits it was made of - and a round over either would call a
  head clean that is not the branch's. The round is refused with
  `BRANCH_DIVERGED`, the refusal the next build gives, naming the build lease
  and how to integrate the two there. jig merges nothing that is not its own;
  merging origin's branch into the build lease leaves the lease ahead of origin's,
  and the round reviews it;
- the two have diverged, and the build lease's copy holds no commit jig built
  that origin lacks: the copy is not jig's to keep, by the rule the build's
  re-cut applies to the same lease (`pool.HoldsUnpushedBuilt`), so what it
  holds of its own is an attempt's leftovers, the history a rewrite replaced,
  or leftovers on top of jig's commits that origin already has, and origin's
  copy holds both the author's commits and whatever of jig's was pushed. It
  counts as a build lease that does not hold the branch;
- this machine's build lease does not hold the branch: origin's copy.

Whichever copy that is must then hold every commit the journal records jig
built (`pool.RequireBuilt`), or the round is refused (`BUILD_LEASE_MISSING`):
the commits are on another machine, or lost, and a review of a copy without
them would leave them out and say nothing. It is one rule for every copy, not a
rule for the case of no lease. A build lease cut afresh on this machine, after
another machine built, holds the branch as origin has it and lacks them too; so
does one that built commits of its own without them.

The build applies the same rule to its own lease before it dispatches a slice on
an adopted branch: the lease must hold every commit jig built, or the run is
refused with `BUILD_LEASE_MISSING`, since the next commits would go on a branch
that leaves the earlier ones out. The help is the same on both sides: run the
command on the machine that built them, or push them from there, and this
machine follows origin's copy. When the author has rewritten the branch under
commits jig built, that machine's lease has diverged from the rewritten origin
and is refused as such; merging origin's branch into the lease, as that
refusal says, keeps the old tip reachable, so the commits jig built and the
start sha they descend from stay valid, and pushing the result lets the other
machines follow. Rebasing jig's commits onto the rewritten branch is not a
remedy: it changes the commits the journal recorded, and the start sha is not on
the new history. The commits are found by id, so a human who rebases or squashes
jig's commits before pushing them hides them from the same check:
`BUILD_LEASE_MISSING` names them and says to integrate with a merge instead.

The author pushing between rounds is followed while jig has built nothing on
the branch: each round reviews origin as it is now, a rewritten history
included. Once jig has commits of its own on the branch that no push has taken
to origin, an author pushing again leaves the two diverged: the rounds and the
next build stop with `BRANCH_DIVERGED` until they are integrated in the build
lease, and the rounds review origin's copy from the moment it holds jig's
commits.

## An adopted ticket's intent

Adoption changes nothing about how a round's intent is resolved. It follows the
precedence every ticket's does, the brief, else `intent.md` (`--intent`,
`--doc`, or inferred), else none, and the report says which one the round used
([ADR 0012](0012-intent-provenance.md)). An adopted ticket usually has no
brief, and a branch built outside jig is the case inference exists for: the
round's scope diff is the branch against its merge base with the target, and the
session that matches its files is the author's own. So the first round on an
adopted branch infers its intent like any brief-less ticket's round, records it
in `intent.md`, and later rounds read that. Inference needs nothing special for
an adopted ticket, and a round that infers nothing reports `none` with the
reason, as any other does.


## Publishing a branch that is already on origin

`jig publish` used to take the branch for its own: it squashed everything the
ticket's branch holds beyond the target into one commit, and refused a range
that had already reached a remote. That is right for a `jig/<ticket>` that was
never pushed. It is wrong for a branch whose commits are on origin - an adopted
branch, the author's commits included - and it is why a ticket published once
could not be published again. Four rules replace it. None is about adopted
branches; an adopted branch is the case that needs them all.

**Only unpushed history is squashed.** A branch that is on origin - the test
`reconcile` already makes, an `ls-remote` of `refs/heads/<branch>`, whose
answer picks the squash too - is pushed as it is. The commits already there
keep their shas; jig's own commits on it (the fixes it built, the merge of the
moved target, the memorize commit) sit on top; origin's copy is fast-forwarded.
The report says "not squashed (branch already on origin)" and the journal
records the squash as `none:branch-on-origin`. A branch that was never pushed is
rebased and squashed as before, and the squash still refuses a range whose
commits reached a remote under another name (`PUSHED_RANGE`).

**Publish ships what was reviewed.** The head publish would ship, read from the
publish lease after it is pointed at the copy the gate reviewed and before
reconcile adds anything to it, must be the head the last clean round reviewed
(`reviewed_sha`), when that round recorded heads: otherwise a commit that landed
after the round - another `jig run`, a hand edit, the author pushing - would go
out under a verdict that never saw it. Publish is refused with
`PUBLISH_UNREVIEWED_HEAD` and help to run the gate again. A scripted round
records no head at all and has nothing to hold publish to, so it is let through
as it always was; a round that recorded heads, but none for this repo (the repo
was renamed since), reviewed something else, and is refused like a different
head. The copy is chosen by the function the gate uses (`pointAtTicketBranch`),
so the two cannot disagree: an adopted branch as origin has it while jig built
nothing on it, and otherwise whichever copy holds jig's commits
(`chooseBuiltCopy`, with the same `BRANCH_DIVERGED` and `BUILD_LEASE_MISSING`
refusals): the build lease's while origin has no copy of the branch, as the
ticket's own `jig/<ticket>` does before its first publish. A branch jig built
nothing on needs no build lease at all.

**A push is a fast-forward or it is refused.** A branch already on origin must
be a descendant of origin's copy where publish would push it, and publish never
forces. The copy publish ships was compared with origin's when the lease was
pointed at it, and is refused there (`BRANCH_DIVERGED`) when neither holds the
other, so what is left for the check is a push since: publish fetches again
right after the lease is pointed, and someone can push in between. It checks
that before its first store write, and before it reconciles - the merge of the
target into a branch that cannot be pushed could conflict, and the conflict
would hide the real problem behind `CONFLICT` - so a refusal
(`PUBLISH_NOT_FAST_FORWARD`, with the commits on each side counted and where to
integrate them) leaves the store as it was, instead of failing at git's own
refusal after the journal, the changelogs and the ledger were written. Where
to integrate them depends on whose copy publish would ship: the build lease's,
where jig's commits wait, is where origin's copy is merged in; origin's own has
nothing to merge into, and a round over the branch as it is now is the way on.
The same holds for the lookup below: everything that can refuse a publish for a
reason outside the store comes before the store is written, and the deferred
push of the store from a failure later on is unchanged.

**An existing pull request is updated.** The tracker adapter has a second
optional capability beside `PRCreator`, `PRUpdater`: find the open pull request
from the branch into the target, and replace its body. The github adapter does
it by asking the pull requests endpoint (`gh api repos/<owner>/<repo>/pulls`) for
the open ones into the target from the qualified head `<owner>:<branch>`, which
GitHub applies itself, and `gh pr edit --body-file`. `gh pr list --head` takes
the bare branch name and lists every fork's branch of that name, a page of 30 at
a time, so a lookup that filtered afterwards could miss the repo's own pull
request behind a page of forks', read that as "none", and open a second one that
GitHub refuses. Two found is an error, not a pick. A lookup that fails refuses
the publish - "none" would open a second pull request - and the tracker is built
before the push rather than after it. The
title, base and everything else of the pull request stay as they are, but its
body is replaced by the one publish wrote, so a description its author wrote by
hand goes with it. The confirm question, the report (`pr_url`'s `action` column)
and the journal's `pr` line say opened or updated. A tracker with no `PRUpdater`
is asked to open one, as before.

Only an open pull request from the branch into the target is the one publish
updates. A closed one is a decision about that pull request, not about the
branch's next delivery; a merged one is delivered history; one into another
base is another delivery (a stacked pull request, say). None of them is found,
so publish - which the operator asked for, and whose question says it would
open a pull request - opens one into the target and touches the others not at
all. Refusing or asking instead would make every publish depend on the
tracker's whole history of the branch, where the question asked here is only
whether there is a pull request to update.

**The memorize commit is a jig commit like any other.** The retrieval notes
(`.claude/retrieval/<ticket>.md`) are committed on the branch by publish, and on
an adopted branch they land on top of the author's commits and stay a commit of
their own instead of folding into a squash. They are jig's addition to a branch
someone else built, in a directory the author may not have; the merge of the
target, when it moved, is another. Both are pushed with the branch, and the
author's commits are not touched. The commit holds the notes and nothing else:
the publish lease is put back at its head before they are written, since the
oracles it may have run (when the target moved) can leave files behind that are
no part of the branch.

Publishing a branch does not change what the ticket's leases hold, and the
sequel depends on how the branch got to origin:

- Pushed as it is, with jig's commits in it under their own shas: the build
  lease's copy is now behind origin's, which the sync rule fast-forwards at the
  next build acquire. The gate reviews origin's copy and publish ships it, for
  the ticket's own `jig/<ticket>` as for an adopted branch, since the rule that
  chooses the copy is the branch's: origin's holds every commit the journal
  records jig built, and the merge of the target and the memorize commit publish
  added, which the lease's copy lacks. The loop goes on: another round, another
  fix, another publish, each a fast-forward that updates the same pull request.
- Squashed, the first publish of an ordinary ticket: the build lease keeps the
  unsquashed commits, diverged from the squash on origin, as before. The gate,
  the build and publish refuse it with `BRANCH_DIVERGED` (the lease holds
  commits jig built that origin lacks), naming the way on - merge origin's branch
  into the build lease (`git -C <lease> fetch origin && git -C <lease> merge
  origin/<branch>`) - after which the lease is ahead of origin's and a publish
  from there pushes as it is, the squash staying where it was pushed. Publish
  does not re-point the build lease at what it pushed: that writes to a lease
  publish does not own, after the push has shipped, where a failure has nowhere
  honest to go.

`jig status` names `jig publish` after a clean round for an adopted ticket as
for any other, and `jig solve` publishes it.

## No brief to amend

A builder can report a flawed brief. Amending the brief and requeuing (`jig
requeue --from-brief-diff`) is the remedy only for a slice that cites brief
sections to amend; for any other slice the finding becomes a plain question,
answered like any other, instead of an instruction to amend sections that are
not there. That is every slice of a ticket with no `brief.md` - an adopted
ticket is one - and a gate fix slice on a ticket that has one. `jig status`
resumes a parked slice by the same rule. `jig requeue --from-brief-diff` on a
ticket with no `brief.md` is refused with the slices' own remedies (`jig
requeue --slice`, answering the question) instead of failing on the missing
file. The journal still records what the builder reported.
