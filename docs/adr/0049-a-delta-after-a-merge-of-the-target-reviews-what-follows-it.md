# A delta after a merge of the target reviews what follows it

[ADR 0038](0038-a-published-stacked-ticket-can-be-behind.md) let a head be reviewed through merges git itself would make, and let a delta round whose tip is a hand-resolved merge review only the resolution. A line that went on past the merge with an ordinary commit fell outside both: it was the plain delta from the reviewed head, so a one-file fix after a clean merge of the target asked the reviewer to cover every file the target had brought in. The merge is not the author's work, and the reviewer had no way to tell the fix from the target's files.

## Decision

**A line of merges of the target, then the branch's own commits, is a line of its own.** From the reviewed head, the line starts with one or more two-parent merges, each of whose second parent is reachable from the target ref (`origin/<target>`, or the branch of the parent a stacked ticket goes into), and every one but the last has a tree equal to git's own merge of its parents. The last of them, the carried merge, is clean or resolved by hand. After it come single-parent commits and nothing else. `classifyLine` returns this kind (`lineMergedThenOwn`) with the carried merge's base.

**The round reviews the diff from the carried merge to the head.** The base is the carried merge's commit when its tree is git's own merge, and git's own merge of its parents, a tree, when a person resolved it by hand. The diff then holds the later commits' changes and, for a hand-resolved merge, the hand-resolved hunks. `must_review` is the files of that diff and the files of open findings that still exist, so nothing the target brought in is listed. The scope stays `delta`. This amends the hand-resolved paragraph of ADR 0038, which stopped at the tip.

**A later commit on a file the merge brought in is read for its own diff.** The file is in `must_review` because the commit changed it. The lines the target brought in are not what the reviewer is asked to read, and a hand-resolved merge's hunks are, wherever they are.

**Every other line stays the plain delta from the reviewed head.** A merge after an ordinary commit, an octopus, a merge of a branch that is not the target after the carried one, a hand-resolved merge that is not the last of the leading merges, and a conflict committed with its markers all stay `lineOther`, so the reviewer sees more, never less. The head of this kind is not reviewed: `headReviewed` and the carry-over treat it as they treat `lineOther`, and publish refuses it until a clean round reviews it.

**The review notes name the carried merge.** The round records, per repo, the merge's commit and the ref it brought in in `report.yaml` (`carried_merge`, absent for any other round and for a report an older jig wrote). `ReadRounds` reads it, and the reviewers line of that round ends with "the merge of <ref> at <short sha> was carried, not read", prefixed by the repo when the ticket spans several. With several merges in the leading run the last is named; the earlier ones are its ancestors along first parents. The open-round record carries the same fact so a round opened by `jig gate` and closed by `jig review done` records it.

## Consequences

- Like the line of merges ADR 0038 covers, this kind asks git to merge, so an older git than 2.38 stops it with `GIT_TOO_OLD`. A line that starts with an ordinary commit still never reaches git's merge.
- `base_sha` in a delta round's `review.json` and open record is a commit or a tree, as it already could be.
- The reviewer's prompt, the carry-over and the publish checks are unchanged.
