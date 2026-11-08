# A brief edit or a new slice does not rebuild: jig slice add and jig validate --restamp (BRIEF-9)

The GATE-7 run hand-edited `slices.yaml` to add slices and to repair stamps after small brief edits. The brief left these calls to the build, all defaulted on 2026-11-07.

## jig slice add

- **Flags.** `--id`, `--goal` and `--oracle` are required. `--workspace` defaults to `root` when the project has one repo and is required otherwise. `--from-brief` defaults to `Seams`, because the intake rule makes Seams every slice's anchor, so an unrelated decision edit never makes the slice stale; `all` stamps every section, or a comma list of headings, each of which must exist.
- **Checks.** The slice-level rules of `jig validate` (oracle, `blocked_by` ids, cycle, workspace, the reserved id `gate`) run on the ticket's slices plus the new one before anything is written, and report only what the new slice causes. A duplicate id is the store's `SLICE_ID_DUPLICATE`, from the same append the gate's fix slices use.
- **Approval.** Not touched. The approval covers the brief, not the slice list, so a slice added to an approved ticket stays buildable. The next line points at `jig validate <T> --check`.
- **Journal.** A new `slice-add` event, ref the slice id, beside `fix-slice`.

## jig validate --restamp

- **What it restamps.** A slice whose `from_brief` cites a hash no current section has gets the current hash of the section that hash was the hash of. The old hash is resolved to its heading by walking the store's git history of `brief.md`, newest first, to 200 revisions. A slice stamped from several sections keeps the ones that are current. Only `from_brief` changes: state, attempts, question and the verified commit are untouched, so a green slice stays green.
- **Unresolved.** A hash no committed revision holds, or whose heading is gone, is listed under `unresolved` and never guessed. The slice is left as it is, the rest are restamped, and the command fails as a validation error.
- **Building.** A stale slice that is `building` refuses the whole restamp (`SLICE_BUILDING`), writing nothing: a builder works against the brief it was handed, and `jig requeue` is the way to tell it.
- **Approval.** Not recorded, not combinable with `--check`. Editing the brief withdrew the approval; the person confirms the edit with `jig validate <T>`.
- **Journal.** One `restamp` line per restamped slice; the store is pushed once.

Rejected: recording the heading beside each hash in `slices.yaml` (the wire shape stays as it is, history gives the heading when needed); a restamp that approves (it would confirm a brief edit nobody read); a restamp that requeues (that is `jig requeue --from-brief-diff`, which runs first when the work must be redone because it needs the stale stamp to find the slice).
