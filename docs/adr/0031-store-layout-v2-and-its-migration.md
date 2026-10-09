# Store layout v2: tickets/<id>/, schema_version 2, and jig store migrate

A store's real tickets used to sit directly at the store root, next to
`charts/`, `platform/`, `ledger.md` and `project.yaml` - a holdover from
before the store carried anything else at its root, and one that no longer
scales: a sizeable store's root listing is dominated by ticket folders,
and nothing told `project.yaml`'s readers which `schema_version` a given
store actually followed, since `project.yaml` declared the field but
nothing ever checked it.

Two decisions of the store-layout map settle this (chart decisions 1, 6, 9
and 13): real tickets move under `tickets/<id>/`, and `schema_version` is
enforced from the one place every command loads `project.yaml`. Both
landed with an area key on every id (ADR 0028's `ticket_format` replaced
by `keys:`, an earlier slice of this same ticket): an id is `<key>-<n>`,
and a v1 store's ids, minted under one `ticket_format` string, already
happen to be shaped the same way (a key, a dash, a number), which is what
lets `jig store migrate` read them with the same pattern it reads a v2
store's.

## Layout v2

`Store.TicketDir(id)` is the one function that names a ticket's folder,
`tickets/<id>`, and `Store.TicketRelDir(id)` its store-relative form, for a
caller that shells out to git against the store. Nothing else joins
`tickets` and an id itself. `Store.TicketIDs()` lists every id-shaped
folder under `tickets/`, under any key, sorted by key then number - the
one function minting's per-key counter, the GitHub mirror, and every other
listing that shows many tickets read tickets through. A ticket folder's
own contents (brief, slices, journal, gate rounds, changelogs, `pr/`, and
A6's `tracker/github.yaml`) are unchanged by the move.

`project.Config.CheckSchemaVersion`, called once from `cmd/jig`'s
`resolveStore` (the one place every command loads `project.yaml`), refuses
a `schema_version` newer than 2 outright (jig itself would have to be
upgraded to understand it), and one older than 2 for every command except
`jig store migrate` - whose whole job is opening exactly such a store and
rewriting it onto schema 2 - and `jig help`/`jig version`, which never
resolve a store at all. A schema-2 `project.yaml` refuses `ticket_format`
and `tracker:` outright, since the migration is what rewrites them into
`keys:` and `trackers:`; the v1 reads that once made sense of them (`L1`'s
`tracker: local`, `L2`'s `ticket_format`-as-key, `MintLocalID`, and the
mirror's own read of `tracker/ticket.md` history) are gone with them.

The alias scan `Store.ResolveTicket` reads (`Store.aliasClaims`, memoized
per `Store` value) is dropped not only after jig's own `ticket.yaml`
writes but also after a checkpoint's pull: the migration's whole point is
to make a renamed ticket's old id resolve as an alias, and a store that
pulled the migration's commit from another clone must see that alias
resolve at once, not only after its next write of its own.

## jig store migrate

Older jig builds cannot be made to refuse a v2 store on their own, since
they never checked `schema_version` at all - so the upgrade is a
one-time, operator-run procedure, not something jig enforces across every
clone at once (chart decision 13). `jig store migrate --map <file>` reads
an operator-supplied rename map - `keys:` (the new area keys and their
meaning) and `tickets:` (every existing ticket's old id to its new key) -
and refuses a ticket the map leaves out, one the store does not have, an
undeclared key, or a map that declares the key the store's old ids
already carry (every old id stays claimed as an alias, so a new id minted
under that key later could otherwise name another ticket's own old id).
Each key's tickets are numbered in old-id order by the old id's own
number, so the lowest old id under a key becomes `<key>-1` regardless of
map iteration order. `--dry-run` runs every one of those checks, then
prints the resulting rename (old id, new id, title) and every file the
migration would move, delete or rewrite - without changing anything - so
an operator can review the plan before committing to it.

The apply adds the refusals that only matter once something is about to be
written: the store must be at schema 1 (nothing else is this command's to
migrate), clean, and level with its origin (ahead, behind or diverged are
all refused, since the migration's own commit must not bury or be buried
by anything), and this machine's lease pool must hold no lease - build,
gate or publish - for any ticket the map renames, since a lease still
checked out under an old id would be orphaned by the folder move. It warns,
without refusing, about any `jig/<old id>` branch still on a repo's origin:
jig works that ticket on `jig/<new id>` from here on, so that branch should
be merged or closed first.

Each ticket's title and description are resolved with the same four-rank
fallback the GitHub mirror's `ResolveTicketTitleBody` already applied
(`ticket.yaml`, a chart entry, the latest committed `tracker/ticket.md` not
written by publish, the id itself) - on the v1 store, before its folder
moves or `tracker/ticket.md` is deleted. That fourth rank, reading
`tracker/ticket.md`'s git history, moves out of `internal/mirror` into the
migration's own code (`internal/migrate`): once every store is migrated,
every ticket's title and description already live in `ticket.yaml`, so no
command but this one-time migration ever needs that history again. The
migration writes the resolved text into the moved ticket's `ticket.yaml`,
alongside the old id as an `aliases:` entry and `blocked_by` refs
translated to the renamed ids, deletes the local tracker's now-redundant
files (`tracker/ticket.md`, `tracker/subtasks.yaml`, `tracker/comments/`),
and keeps `tracker/github.yaml`. It rewrites `project.yaml` (`schema_version:
2`, `keys:` from the map, `tracker:` renamed to `trackers:`, `ticket_format`
removed, every other key left exactly as it is) and every chart's
`tickets.yaml` (entry ids and `blocked_by` refs), and commits everything
once, on the current branch, with jig's own store identity - like every
other jig store commit - without pushing or syncing a tracker. It then
prints the next steps: push the branch and review it before merging; the
first sync after the merge updates each mirrored issue in place from its
moved record.

## What stays out of scope

Rewriting any other history file (journals, gate rounds, changelogs, PR
notes, `ledger.md`, `platform/`, a chart's `map.md`) is unnecessary: every
one of them is read by id, and an old id resolves through its alias.
Branches in the product repos, and the machine's evidence tree (keyed by
old ids, which belong to tickets in flight the migration's own branch
warning already tells the operator to finish first), are the operator's to
carry forward, not jig's to rewrite. jig's own store's migration - the
owner's rename map and the rehearsal-then-cutover procedure of chart
decision 13 - runs after this ticket merges, using the command this ADR
describes.
