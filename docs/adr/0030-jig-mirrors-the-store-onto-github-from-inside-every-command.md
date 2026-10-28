# jig mirrors the store onto GitHub from inside every command

**Amended** (see "Amendment: a checkpoint lands what its sync wrote" below): the records a sync writes after a ticket's creation are committed and pushed by the checkpoint that ran the sync, instead of waiting for the next checkpoint's commit, and `jig trackers sync` pushes what it writes. The mirror's shape, ownership and drift rules below stand.

A bridge program outside jig used to project the store onto a GitHub
Project and its issues: one ticket or chart, one issue; the project's
Status and Store ID fields; sub-issue and blocked-by links; pull requests.
It could not run while jig used the store, so the board moved only between
jig commands, and a store the bridge was never pointed at never reached a
board at all - a design flaw, by the owner's own call (2026-10-04). L1
(T-23, develdeco/jig#102) made the ground safe to build this on: jig mints
and claims every ticket id itself (ADR 0028), the tracker package is gone,
pull requests come from `internal/repohost`, and `project.yaml`'s
`trackers:` list replaces the single `tracker:` key.

## The mirror is jig's own, run from inside every checkpoint

`internal/mirror` makes the bridge's job jig's own: `Store.AfterCheckpoint`
runs a full sync in the same process as every checkpoint (`Store.Push`),
best-effort - a GitHub failure prints one warning and never fails the
command, and the next checkpoint retries. `jig trackers sync` runs the same
sync on demand, for an operator to watch what it would do or confirm it
settled. One ticket, one pull request cuts the bridge over in one piece,
since the two could never both write the same issues at once.

`internal/mirror` holds what any tracker provider would share (the
`trackers:` entry, the checkpoint hook, a sync's own scheduling);
`internal/mirror/github` is the one GitHub client, a thin GraphQL wrapper
over the mutations and queries the bridge already proved. The client's
endpoint and token source are both arguments (`internal/mirror/github/client.go`
- no environment variable redirects either, per `develdeco/jig#84`), so
every test points it at a fake GraphQL server and none ever reaches
github.com. It paces itself the way GitHub asks a client that creates
content to - one mutation per second, and four attempts with doubling
backoff for anything that comes back a server error or a rate limit -
since a first sync of a real store opens an issue per ticket and chart and
would otherwise be the exact shape GitHub answers with a secondary rate
limit, abandoned mid-way with issues left carrying footer-only bodies.

What a sync found is reported through the writer of whatever command ran
it, not just `jig trackers sync`: a checkpoint that overwrote a hand edit,
skipped a ticket on a publish-safety hit or could not link a blocker says
so in that command's own output. A sync whose reporting only reached an
operator who thought to run `jig trackers sync` afterward would make the
ownership promise printed in every issue footer unverifiable in practice.

## Until the store's layout moves, the mirror keeps the bridge's shape

L2 to L4 bring the store-layout tickets after this one (area-key ids,
`tickets/<id>/`, the tracker tree); until then, the mirror keeps its
records exactly where the bridge kept them - `<ticket>/tracker/github.yaml`,
`charts/<name>/github.yaml`, in the bridge's own keys - so the bridge's
records are adopted as they are rather than migrated, and a later ticket's
layout change carries the mirror along rather than this one reaching ahead
of it.

A new issue's creation is claimed like a ticket id (`Store.Claim`): a
commit of the new record alone, pushed at once. Two clones racing to open
the same ticket's or chart's issue cannot both win: the losing push is
rejected, its pull brings in the winner's record, and the losing clone
closes its own issue as not planned (`Duplicate of #<number>`), drops its
own record, and adopts the winner's - rather than retrying into the same
race again. Every other record write (an update, a link, a board
placement) lands in the working tree with no race protection of its own,
since only a brand-new id needs the claim's, and goes out when the
checkpoint's sync is done (the amendment below).

## Ownership is split field by field, and an edit on the other side is drift

jig owns each issue's title, body, open/closed state, parent and
blocked-by links, and a board item's Status and Store ID - exactly what the
bridge owned, and nothing more: labels, assignees, other fields, comments
and views are the repo owner's, untouched. The record's `synced:` keeps
what the last sync itself wrote to every field jig owns, under the
bridge's own names, so the next sync can tell an edit made on GitHub
(`synced:` and GitHub's current value disagree - drift, printed and
overwritten with the store's value) from a change made in the store
(GitHub still matches `synced:` - applied with no drift line). A record
adopted from the bridge, which kept no `synced:` of its own, gets the
benefit of the doubt on its first sync: whatever GitHub already shows
becomes the baseline, with no retroactive drift report - the critical path
the cutover itself depends on ("a store whose records match GitHub syncs
with no mutation"). A recorded issue found gone from GitHub (deleted, or
its repo gone) is simply opened again, rather than failing the sync.

## Consequences

- The mirror adds one more best-effort step to every checkpoint: a store
  with no GitHub tracker configured pays nothing beyond a no-op check, and
  one that does pays a bounded sync (2 minutes) that never blocks the
  command's own result on GitHub being reachable. `jig trackers sync` runs
  with no such bound - it is run on demand rather than from inside another
  command, and at one mutation per second a first sync of a sizeable store
  needs more than 2 minutes to finish - so a bounded sync that runs out of
  time names how many tickets or charts still have no issue and points at
  `jig trackers sync` instead; the cutover and an L3 migration each run one
  full `jig trackers sync` rather than waiting out the backlog.
- A project's board and issues can now be trusted to reflect the store
  exactly, as of the last sync: any edit made directly on a jig-owned field
  is both visible (the drift line) and short-lived (overwritten at the
  next checkpoint). Board edits are not a way to redirect jig's state - the
  store remains the only source of truth, by construction, not by
  convention.
- This supersedes the bridge program as this project's GitHub mirror, in
  one cutover: the bridge's own `tracker/github.yaml` is deleted once
  `trackers:` names the same repo and project, and the first real sync
  afterward reports zero changes and no drift when the two agree, which is
  how the operator confirms the handoff.

See [DECISIONS.md](../../DECISIONS.md) for the records' exact keys, the
renderer's golden format, the publish-safety scan, and the drift slice's
own mechanics.

## Amendment: a checkpoint lands what its sync wrote

Recording the README demo against a store with an origin and a mirror found
the gap in "goes in with the next checkpoint's commit": `jig publish --pr` is
a ticket's last checkpoint, so no next one comes. The pull request's record
(its `prs:` entry, `synced.status`, `body_sha256`) stayed uncommitted until a
later `jig trackers sync` ran `Store.Sync`, which commits leftovers but never
pushes, so a teammate's clone and the store on GitHub never saw it.

- **The checkpoint that ran the sync lands its writes.** The tracker tree's
  writers (`WriteTrackerRecord`, `RemoveTrackerRecord`, `LinkTrackerEntry`,
  `RebuildTrackerLinks`, `DropLegacyTrackerRecord`) note the paths they change
  in the working tree, and `Store.RunCheckpointHook` ends by committing exactly
  those paths and pushing (`landTrackerWrites`). The commit is the person's,
  `jig: record the tracker mirror's sync`, scoped by pathspec the way `Claim`'s
  is, so any other dirty state in the store (a journal line another process
  just wrote) stays where it is. A sync that failed part-way lands the records
  it did write, which are true. A landing that fails (the origin cannot be
  reached, a rebase conflicts) is one warning, and the records stay for the
  next checkpoint's commit, as before.
- **An up-to-date store costs nothing.** `WriteTrackerRecord` leaves a record
  that already holds the bytes alone and notes nothing, so a sync that finds
  nothing to change makes no commit, no push and no git process: only a
  checkpoint whose sync changed the tree pays for a landing.
- **Issue creation keeps its claim.** A new issue's record is still committed
  alone and pushed at once (`Store.Claim`), with the same race protection; the
  landing only carries what the sync writes after it.
- **`jig trackers sync` pushes what it writes.** After `Store.Sync` and the
  mirror's sync, a run that is not a dry run calls `Store.LandTrackerWrites`,
  which commits the noted paths and pushes the branch whether or not there was
  anything new to commit, so the commit `Sync` made of the leftovers reaches
  origin too. It leaves nothing uncommitted or unpushed; `--dry-run` still
  writes to neither GitHub nor the store, and lands nothing. A landing that
  fails is the command's error.

The observe hooks' own journal lines (a session's `spend` line, the note of a
question left open) are not mirror writes and are not landed this way: they
stay in the clone until the next checkpoint's commit or `jig trackers sync`.
That is on purpose. They are written inside the harness's Stop and SessionEnd
hooks, in the agent's way and under the harness's own bound on how long a hook
may run; a commit and a network push there would put a round trip into every
turn's Stop (the spend line is cumulative and written again whenever the total
moved, so a push per Stop is a store commit per turn) or be cut off at session
end. Nothing is lost by waiting: the line is cumulative, there is nothing to
reconcile, and the next checkpoint's commit sweeps it up. A session that ends
and leaves its clone idle leaves its last spend line local until a checkpoint
or `jig trackers sync` runs there. A detached flush at session end, as
verification runs detached workers, would remove that wait; it is not built.
