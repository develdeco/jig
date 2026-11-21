# The gate notes a store reference and never refuses

A change can add a line that names something only the jig store holds: a ticket id in a comment, a finding id in a test name, the store's own path in a fixture. A reader of the repo cannot open any of them, and the repo stands alone from the store. Nothing noticed such a line before a person happened to read it.

## Decision

**A store reference is an exact match against the strings jig holds.** A ticket id or alias of any ticket in the store, an id of a finding the ticket's own earlier rounds reported, and the store's own root path. There is no pattern for "looks like an id": the store's keys are not known to the code, and a pattern flags prose that is not an id. An id matches as a whole token, not flanked by a letter, a digit, a hyphen or an underscore, so it never matches inside a longer id or a word, and it is case-sensitive. The store's path is matched as it is spelled, after backslashes (single or doubled) become slashes, in the form given and with its links resolved; a filesystem root is never matched. Another ticket's finding ids are not matched: they are not names a reader of this change could mistake for the ticket's own.

**Only added lines count.** The lines the ticket's whole change adds, from the base a full round uses, in every repo of the round, whatever this round's scope (`gitx.DiffAdded`: added lines with their path and new-side line, no deleted line, binary file or submodule). A delta round therefore still notes an earlier addition until the line is gone. A deleted line and an unchanged one never count.

**It is a fact of the round, never a reason it is not clean.** The round records the references on `report.yaml` as `store_refs` (repo, file, line and what was named, in diff order, every one of them), and the verdict follows the findings alone, as it does for independence ([ADR 0046](0046-a-rounds-independence-is-recorded-not-enforced.md)). Nothing is refused, parked or routed to a fix slice, and a person may cite an id on purpose. The references are computed when the round opens and carried in its `review.json`, so a recorded round closes with the ones it opened with. A carried round copies the references of the round it carries, because none ran; a scripted round (the test double) records none. A hit on the store's path is recorded as `<store path>`, never the path, since the store is shared by machines that spell it differently.

**Where it shows, from the one field.** `jig gate` prints a `store_references` table (repo, file:line, what was named) when the round has any, at most 20 rows and then an "and N more in K files" row, and `jig review done` adds a count beside the verdict. `jig status` keeps its rounds columns and lists the rounds' references in a `store_references` block under them when any round holds some. The pull request's review notes name the count and the file:line of each in the repo, and not what they cite, since the notes are public. A round an older jig recorded has no `store_refs` and shows nothing.

**Issue numbers are not matched.** A mirrored issue's number is in the same number space as a pull request's, which a repo may cite freely, so jig cannot tell a store reference from the code's own history.

**Chart names are not matched.** A chart's name is a slug that collides with ordinary words, so matching it would flag prose. Ids and the store's path catch what leaks without false hits.

**Test data that equals a real id gets no special path.** It is noted like any hit, because a rule for fixtures (a path, a directory name) is not a general mechanism, and the note never blocks. Fixtures use made-up ids.

## Rejected alternatives

- **A reviewer-style finding of its own kind.** A finding is a judgment with triage, recurrence and clearing; this is computed from the diff and clears itself when the line is gone.
- **A refusal at publish.** It contradicts recording rather than enforcing, and a person may cite an id on purpose.
- **A pattern for "looks like an id".** It flags prose that is not an id and misses an id shaped otherwise.
- **Matching issue numbers.** See above.

## Consequences

- `report.yaml` gains `store_refs`, `review.json` gains the same list, and `gitx.DiffAdded` is new; both fields are omitted when empty, so a store written by an older jig reads unchanged.
- `CONTEXT.md` says what a *Store reference* is.
