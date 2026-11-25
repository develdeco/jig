# A gate round reads the whole change, and runs a test only to prove a finding

ADR 0036 made every round probe each term of the changed code on the intent's critical paths, so a missing test could not slip through a read. On the chart runs that followed, an independent reader of each finished change still found about two medium or worse defects per ticket after a clean gate, 30 of 35 of them in code the first round had read. The reviewer spent its effort on the sweep (3,050 probes over 83 rounds; in one chart run about 27% of the reviewer's tokens and 38% of its time) and ranked low what it did see: it had already raised 8 of the reader's finds. The reader reads the whole change against the brief, ranks each finding by it and proves it, with no sweep.

## Decision

The rules are jig's method, the same for any project.

- **A full-scope round** reads the whole change against the intent, as a reader judging the finished change would, not term by term. It looks for wrong code and states of the intent's rules that the code never handles; what the change breaks elsewhere (callers, pinned output in other packages, docs that now say something false); the repo's own rules, as its agent instructions and contributing guide state them; and cost that grows with real data.
- **Each finding is ranked against the intent.** `high` is wrong behavior a person meets, `medium` is wrong in a case the intent names or a broken repo rule, `low` is worth a note. These replace "how much harm follows if this part is wrong". Routing is unchanged (ADR 0015): by default low findings become notes.
- **A finding is proved.** It names its file and line, and its detail proves it by its kind: a defect states the input or state and the wrong outcome, a broken rule cites the rule and where it is stated, an ask names the decision it needs. A test gap keeps its proof below. A finding the reviewer cannot prove that way it does not file.
- **A delta round** checks that each fix fixes its finding and that its delta breaks nothing. It never searches outside the delta; a defect it meets there anyway it reports at its rank.
- **A test runs only to prove one finding** the reviewer is about to file: the narrowest command, against a mutant of one term for a test gap, or to settle a suspected defect that reading cannot. Never an oracle, a suite or a sweep over terms. A mutation is made in the round's worktree and put back, never in the clone.
- **The probe record stays, as an option.** A probe that proves a finding may be recorded with `jig probe`. Its record (`probes` in the result and in `findings.yaml`, `probed` in `review.json`, each current or stale, and the counts and tables of `jig status`, `jig tui` and the review notes) is unchanged. Removing it is a later decision, taken only if this one is kept.
- **The sweep's work list goes.** With no sweep, every hunk would read "unprobed", and that count on every pull request would look like a failure. `review.json` loses `hunks`, `jig probe` loses `--skip` and `--why`, a result that names `skipped` is refused as any unknown field, and no output shows a skip or an unprobed hunk. A round recorded before this decision keeps loading, its `skipped` and `unprobed` keys ignored, and a round left open with a drafted skip still closes.
- **Unchanged:** tests at the seams (ADR 0016: a missing test is a problem only at a seam the intent names, or as proof of a reported defect; a finding about a test names the mutant it must catch and the assertion that catches it), at the same three ranks; a finding names the rule broken, with its other instances in the detail and an oracle that proves the rule across them; the `fix`, `ask` and `note` actions; a dismissed finding is never raised again; and `must_review` is covered.

## Where it is said

`skills/review/SKILL.md` has a `## Read` section, tied to the round's scope so that a later full round reads the whole change too, and the ranks sit beside `--risk`. The help lines of `jig review context` state the test-run rule, and they and the help of `jig probe` name it only as that optional record. The reviewer's spawn prompt only names the skill.

## Measure and revert

Judged on the first tickets built wholly after this decision and the change that follows it have landed, over at least five, with an independent reader of each pull request kept as the yardstick. The figure it must move is the reader's medium or worse findings per ticket after a clean gate: from 2.1 to at most 1.0. Reviewed rounds per ticket should fall from a median of 5 to 1 or 2, and the reviewer's test runs per ticket, about 41, should fall. Above 1.5 reader findings per ticket, the change is undone by a revert of its own diff, which brings back the sweep, its work list and the skips; the probe record never left.

## Alternatives rejected

- **Keep the sweep and ask for a whole read too.** The sweep took the reviewer's time and shaped what it looked at.
- **Keep the work list without the sweep.** Every hunk would read unprobed, a number that means nothing and looks like a failure.
- **A step that lists what the change reaches before the read.** It was tried in ADR 0036's amendment and taken out: each fix's reach found new cases of the ticket's own rule around it, and rounds multiplied.
- **Remove the probe record now.** It costs nothing while nobody must fill it, and keeping it keeps a revert small.

## Consequences

- Replaces ADR 0036's sweep, hunk list and skips; its probe record stays.
- ADR 0016's seams rule stands with no per-term sweep over it, and ADR 0017's "run no tests" has one exception, the narrowest test that proves a finding.
- `CONTEXT.md` drops *skip* and *unprobed hunk*, and *probe* is a mutation that proves one finding.
