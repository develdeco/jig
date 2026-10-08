# jig mirrors the store onto GitHub from inside every command

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
placement) lands in the working tree and goes in with the next
checkpoint's own commit, since only a brand-new id needs the claim's race
protection.

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
