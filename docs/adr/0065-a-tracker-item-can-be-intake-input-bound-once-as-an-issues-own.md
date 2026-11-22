# A tracker item can be intake input, bound once as an issue's own

[ADR 0005](0005-tracker-is-a-projection.md) made the store the truth and a tracker only a projection of it, and [ADR 0028](0028-jig-mints-and-claims-every-ticket-id.md) had jig mint every id itself. Work that someone files on the tracker by hand, outside jig, had no way in: the person re-typed it as a new need, and the sync then opened a second issue beside the first.

## Decision

**A tracker item is read as intake input, at intake and at the bind, nowhere else.** `jig chart new <name> --item <ref>` reads one item by number (`github.Client.ItemByNumber`, one query) and makes an issue ticket from it and the chart that plans it, in one claim. The ref is the bare number, `#48`, `owner/repo#48` or the issue URL; an item of another repo than the project's tracker repo, a pull request and a number GitHub does not have are refused before anything is minted.

**The item becomes the issue's own.** The issue takes the item's title and its body unchanged, and the item's number is bound to it by a record written in the same claim that mints it, with no `synced:` key: the first sync updates the item in place from the issue, so no second issue is opened. After the bind the store owns the text. The mirror stays one-way: an edit made on the tracker to a title or a body is drift and is overwritten, and a change of need goes to the issue's `body`.

**One item is one issue.** A second bind of the same item is refused `TRACKER_ENTRY_TAKEN`, naming the issue and its chart. In a store in writers mode the entry is also reserved in the claims log (kind `entry`, the entry `github/<owner>/<repo>/<n>`, the issue's id), so two writers binding one item cannot both win; the loser's folders are undone.

**jig still mints every id.** The issue's id is minted by jig as any other; only the issue is minted bound to an item someone else numbered. When the project's branches are led by GitHub, the branch of the issue is `<n>-<slug>`, from the item's number; otherwise it is named by the store id as any ticket's is.

**Advisories, never refusals.** A closed item is bound, and the output says the first sync reopens it; an item with linked pull requests is bound, and the output says the issue does not take them over.

## Consequences

Amends [ADR 0005](0005-tracker-is-a-projection.md): a tracker item may be read as input at those two points, and the tracker is still a projection after. Amends [ADR 0028](0028-jig-mints-and-claims-every-ticket-id.md): the id is minted by jig, and the item's number is claimed beside it. The claims log gains a line kind; an older jig ignores a line whose kind it does not know. Labels, the author and an item's sub-items are read but not stored, and the mirror leaves an issue's links to other issues alone: it removes a blocked-by edge as drift only when the blocker is an issue the store holds a record for, which amends [ADR 0030](0030-jig-mirrors-the-store-onto-github-from-inside-every-command.md)'s ownership of blocked-by links as far as edges to other issues. The bind says which of an item's blockers the first sync would remove. A bind that fails after reserving its entry in the claims log releases it.
