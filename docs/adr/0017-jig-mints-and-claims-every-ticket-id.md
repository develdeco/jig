# jig mints and claims every ticket id

ADR 0005 let whichever tracker a project configured mint a ticket's id -
the local tracker counted the store's folders, the github tracker minted by
opening an issue, the command tracker ran whatever it was told to. That
tied an id to a round trip the store itself did not need: `jig ticket new`
neither committed nor pushed what it minted, so two clones of one store
could compute the same next id and both write it, and an id minted by
opening a tracker issue could not be retried without leaving an orphan
behind if the write that followed failed. The store-layout destination
(decision 7) settles this the other way: the store is the source of truth,
and a tracker only ever mirrors it. An id is store state like any other, so
minting it must be the store's own, atomic act, not a side effect of asking
some other system to agree.

## jig mints

`Store.Mint` computes the next id from `project.yaml`'s own `ticket_format`
- one past the highest number among the store-root folders the format
matches, the same count the local tracker always did - under the store's
own mint lock, so two mints against one clone can never compute the same
id. It refuses an id jig cannot use (`pool.CheckTicket`: a reserved lease
suffix, or anything that is not a single directory name) before writing
anything under it, and creates the ticket's folder and its first
`ticket.yaml` in the same locked section. No tracker is asked for an id,
and nothing writes `tracker/ticket.md` - whatever `project.yaml` says about
trackers, `jig ticket new` and `jig graduate` mint the same way.

## jig claims

Computing an id correctly on one clone is not enough: two clones can each
mint correctly and still both be right, until one of them lands on the
store's origin. `Store.Claim` is the one way a mint becomes a fact
everyone else's clone will see. It stages and commits exactly the paths its
write produced - never a sweeping `add -A`, and the commit itself scoped to
that same pathspec rather than the whole index - so a claim never picks up
unrelated dirty state, even content a rejected claim's own undo can leave
staged alongside it, under one message naming the id, and, on a store with
an origin, pushes that commit alone:

- a push the origin accepts finishes the claim;
- a push it rejects (the origin moved on: not a fast-forward) undoes the
  commit and the folder it created, scoped to its own paths so any other
  dirty state in the store survives, pulls - a fast-forward in the common
  case, since the undone commit was the only thing the local branch had
  that the origin lacked, but not always: a local change the undo
  preserved can overlap a file the rejecting push itself brought in, and
  Claim then refuses `ID_NOT_CLAIMED` naming it rather than ending in a
  merge or rebase conflict - and mints again, up to five attempts, after
  which it refuses with `ID_NOT_CLAIMED` and leaves no claim behind;
- a push that fails for any other reason (the origin unreachable) undoes
  the claim the same way and refuses at once with `ID_NOT_CLAIMED`, help:
  retry once the origin is reachable;
- a store with no origin claims by committing alone, with no push.

`jig graduate` claims one chart entry at a time through the same function,
reading the chart fresh inside every attempt (so a rejected claim's pull is
picked up before the next mint) and writing the entry's id into
`tickets.yaml` as part of the same claimed commit. A failure partway
through a chart leaves every ticket claimed so far recorded on the origin,
and a re-run continues with the entries that still have no id - nothing
already claimed is re-minted or re-created.

The output of a successful claim, on `jig ticket new` or `jig graduate`
alike, names only the id finally landed: a race between two clones never
surfaces as a merge or rebase conflict, only as a different id on each.

## Consequences

- An id now costs a commit, and on a store with an origin a push, before
  anything else can be written under it. That is the price of the
  guarantee: a ticket folder that exists in the store was claimed there,
  not merely computed by whichever clone got to it first.
- `pool.CheckTicket` is the one gate on an id jig cannot use, run before the
  first write under it, whichever command minted it - `jig ticket new` and
  `jig graduate` share the same rule, where ADR 0005's trackers each had
  their own.
- This supersedes ADR 0005's minting clause: no tracker mints an id, ever,
  and the id-stability justification ADR 0005 built on it ("a ticket has a
  stable id even before any tracker adapter runs") no longer has a
  tracker-minting case to distinguish itself from. ADR 0005's wider claim -
  the store is truth, and a tracker only ever projects it outward - is not
  superseded; this ADR is the sharper form of exactly that for ids.

See [DECISIONS.md](../../DECISIONS.md) for the attempt bound, the lock
path, and the claim commit's exact shape.
