# The reviewer probes the critical paths, and jig keeps the probes across rounds

A gate round after the first reviews the delta since the last reviewed head (`scope: delta`), and `must_review` and `reviewed_paths` prove it read every file the delta changed. Nothing records which behavior a round tested. In jig-demo-shop's PRs #24 and #26 the same fix to `cart.Total` passed every oracle with a test suite that pinned no line total's quantity factor: dropping the factor changed no test result.

- Both round-1 reviewers had `scope: full`, read all of `cart.go`, made one mutation check each (of the rounding term), found that gap and missed this one.
- Both round-2 reviewers read `cart.go` again and mutated the rounding term again to confirm its fix. #24's went on to the quantity factor and found the gap, so #24 took three rounds. #26's stopped there and went clean, so #26 was published with the gap.
- #24's round 3 checked seven mutants, one per term of the function, in one script, in 2m05s: the cheapest of the five review rounds (the others took 1m38s to 2m55s).

The miss was in what a reviewer probed, not in what it read or in the round's scope: a review of the whole change missed it twice. Which mutations a reviewer makes follows what is salient, usually the open finding. The review skill allowed a mutation check but never asked for one, nothing recorded it, and no round knew what an earlier one had checked.

## Decision

**Every round, the reviewer probes each term of the critical paths' changed code, unless an earlier round's probe of it is current.** A term is the smallest piece of code whose change changes behavior: an operand, an operator, a condition, a constant, a call or a returned value. The terms in scope are those of each function the change adds or alters on a critical path the intent names (ADR 0016's seams). An intent that names none puts in scope the functions that carry the behavior it asks for. A probe mutates one term in the round's `worktree`, runs the narrowest test command that covers it (one test or one package, never a whole suite) and puts the file back. The command failing or timing out is `caught`; passing is `survived`.

- A survivor at a seam is a finding by the rules that already hold: a missing test at a seam (ADR 0016), routed by the risk floor and the fix budget (ADR 0015). A survivor elsewhere is at most a note. A mutant that cannot change behavior is no finding.
- Round 1 has no earlier probes, so it sweeps every term in scope. It has just read the whole change, and its fix slice then collects every gap at once instead of one gap per round.
- A later round probes only the terms with no current probe: those in files its delta changed, and any an earlier round skipped. It probes a survivor again to check the fix of the finding the survivor backs.
- Which terms matter, and whether a survivor is a problem, stay the reviewer's judgment (ADR 0007).

**jig records each probe and says which are still current.**

- `jig probe [T] --seam S --file F --line N --mutation M --command C --outcome caught|survived` drafts a probe into the round's drafts (`work/gate.round-N.findings.ndjson`), as `jig finding` drafts a finding. `--seam` names the critical path in the intent's words, `--mutation` the edit in words, `--command` what ran. In a project of several repos `--file` is `<repo>/<path>`.
- `result.json` gains an optional `probes` list of the same six fields. A result without it reports no probes, so result files written before this ADR still parse.
- `jig review done` writes the round's probes into `gate/round-N/findings.yaml` beside `reviewed_paths`, each with the git blob of its file at the round's head, and its output counts them (`probes: N`).
- `review.json` gains `probed`: every earlier round's probes, oldest first, each with its round and a `state`. A probe is `current` while its file's blob at this round's head is the one it recorded, and `stale` once the file changed or is gone. jig compares blobs, not commits, so a rebase or a new full round that leaves the file alone keeps its probes current.
- `jig review context` prints `probed` as a table (round, file:line, outcome, state, seam, mutation), and one help line says how to probe.
- The review notes (`pr/review-notes.md`) list the probes current at the reviewed head beside the file coverage, so a person sees what behavior the clean verdict rests on.

jig checks shape only: every field non-empty, the outcome one of the two, the file in one of the round's repos when drafted and present at head when the round is done, the line not negative. It refuses no round for having no probes, or for a survivor with no finding. The count in `jig review done`'s output and the review notes make a clean round with no probes visible.

**A reviewer runs a test only to probe.** ADR 0017's rule becomes: run no oracle and no suite, since every oracle in `oracles_passed` passed on this head's tree; a test runs only against a probe's mutant. A probe's edit is the one edit allowed in `worktree`, and it is put back: `jig review done` already refuses a lease whose tracked files changed (`REVIEW_INVALID`), and it leaves out the untracked files a test run writes.

## Where it is said

- `skills/review/SKILL.md` gains a `## Probe` section with the duty and `jig probe`, and its two lines on tests and edits change to the rule above. The skill is held to 100 lines and 6000 bytes (`lint/skills_test.go`) and is at 5523 bytes, so existing text is shortened to fit.
- `reviewPromptTemplate`, which the review eval's launched reviewer reads in place of the skill, says the same. "Do not edit files, commit, or push." becomes "Do not commit or push, and edit a file only to probe it and put it back."; "Review by reading: run no tests." becomes "Review by reading, and run a test only to probe."; and one line follows the seams line:

  > Probe each term of every function the change adds or alters on a critical path the intent names, unless review.json's probed lists a current probe of it: mutate it, run the narrowest test command that covers it, put the file back, and record it in probes. A mutant that survives at a seam is a finding.

  The result schema line shows `probes`.
- The brief is unchanged. Its seams already name the critical paths; naming a path's terms there would be listing test cases, which ADR 0016 forbids, and jig parses no brief heading.

## Cost

| | Added work |
| --- | --- |
| Round 1 | One test run per term in scope. In the demo, 7 terms, about 30 s to 1 min. In a real repo, the terms times the narrowest command's time; not measured yet. |
| Later rounds | None when the delta leaves the probed files alone, as a fix slice that only adds a test does; otherwise the terms of the files it changed. Reading `probed`: one line per probe. |
| jig | One blob lookup per earlier probe per round. |
| Saved | Each round a gap would otherwise have taken: a fix slice built, the oracles run, a review. About 3.5 minutes in the demo (#24's round-2 verdict to its round-3 clean), 32 to 35 minutes in MB-1 before ADRs 0017 and 0021. |

Round 1 reports more findings, still bounded by `gate.fix_rounds`, the risk floor (ADR 0015) and the fix slice's size cap. The reviewer's effort per scope (ADR 0023) is unchanged.

## Limits

- A caught probe stays current while its file is unchanged, even when the delta changes the test that caught it. That test is in `must_review`, and a weakened test is a finding by reading.
- A term the reviewer does not see as on a critical path goes unprobed. The probes in the review notes show the person what was covered.
- A repo whose narrowest test command is slow makes round 1 slower. The first real ticket built after this journals its probes per round, and its reviewer minutes are compared with MB-1's and BS-1's.

## Test seams

- **`jig probe` and `jig review done` as a reviewer runs them,** in the style of `cmd/jig/gate_reviewer_e2e_test.go`: a probe drafts; an unknown outcome, an empty field or a file in no repo of the round is refused; the round records its probes in `findings.yaml`, refuses one whose file is absent at head, and counts them in its output.
- **`review.json` as `Gate` hands it to the next round,** in one repo and in a project of several: a probe in a file the delta changed is `stale`, one in a file it left alone is `current`.
- **`jig review context`'s `probed` table,** through its golden.
- **`ParseReviewResult`,** with `probes` and without, and the probe field rules.
- **The review notes,** through their golden: the probes current at the reviewed head.
- **The rendered review prompt,** through `TestRenderReviewPromptMatchesDesignGolden`, and the review skill through `lint/skills_test.go`.
- **The review eval.** A teacher-forced case may carry earlier rounds' probes, which its runner turns into `probed` as `Gate` does; scoring stays on findings. A case rebuilt from the demo joins the corpus: the shop at `1fd962b`, round 1 at `c37e7f5` with gold for both gaps, round 2 at `fbd8f46` handed recorded round 1's one probe. Its live runs are report-only (ADR 0009) and run when the owner asks: about ten reviewer runs, roughly 5M cache-read tokens. The bar: the quantity gap found in round 1 in at least 4 of 5 runs, where both recorded round-1 reviews missed it; found in round 2 in at least 4 of 5; and no fix finding on a control change whose terms are all pinned.

## Alternatives rejected

- **Full scope in later rounds, or a last full round.** Both round-1 reviews were full and missed the gap, and each costs a whole review.
- **One mutation per critical path.** A path has several terms (the demo's two have about seven), and one sample per path picks the freshest term, the one #26's round 2 picked.
- **Probe only in the round that would go clean.** Fewest probes, but a gap found then costs another round: #26 would have gone like #24, in three rounds.
- **A mutation-testing tool as a manifest oracle.** It takes the choice of mutants from the reviewer, but needs a tool and an output contract per language. Worth revisiting if recorded probes turn out too shallow.

## Consequences

- Amends ADR 0016's reviewer contract: a clean verdict carries a probe of each term in scope. Amends ADR 0017's "run no tests": a probe's test is the one exception.
- `CONTEXT.md` gains *probe* and *probed*, and `ARCHITECTURE.md`'s store schema shows `probes` in `findings.yaml`.
