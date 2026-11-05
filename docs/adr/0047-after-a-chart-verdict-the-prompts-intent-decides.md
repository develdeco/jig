# After a chart verdict, the prompt's intent to launch decides

[ADR 0037](0037-a-chart-is-run-from-the-main-session.md) ended a start-to-finish run at a chart verdict and, attended, had the session ask whether to take the chart's tickets on; with "go with your recommendations", or unattended, it only reported. [ADR 0035](0035-start-to-finish-and-recommendations-are-separate.md) keeps the person's moments theirs and makes the session's reading of the prompt the rule, never a list of phrases.

The owner clarified (2026-11-05) that the question was asked too often and too uniformly. A prompt that asks for no phase past the brief does not want a launch, so asking is noise. A prompt that fully hands the work over, with the recommendations and the whole session to the end, is stopped by the question and by the "allow what the builds run" question, since nobody is there to answer them.

## Decision

**A chart verdict still ends a ticket run.** What follows depends on how far the prompt's intent to launch the implementation goes. The session reads it from the person's words; the skill gives examples, never a phrase list, and an unsure reading asks. No Go code classifies a prompt.

- **No intent** (nothing past the brief: "fix this: ...", "implement X", "fix this, go with your recommendations"): report what graduated and what `jig next <chart>` says, name the prompts that launch one ticket ("Take <ticket> to PR") or the whole chart ("Take <chart> to PR"), stop and ask nothing.
- **Intent, not fully explicit** (phases past the brief in a ticket's terms, with or without the recommendations: "take it to PR", "fix this and open a PR"): ask once to confirm the launch the prompt implied, the whole chart or the tickets that carry a named ticket's work, with "not now" as the other answer. Unattended it reports and stops. A ticket's terms ask for a ticket run, a chart verdict ends it, and the recommendations hand over that run's decisions, never a chart run's it did not name.
- **Fully explicit** (the recommendations and the session left working to the end, aimed at the work whatever shape it takes: "go with your recommendations and keep going until it is all done"): start the chart run with `jig skill chart`, no question, saying why in one line.

**A run with nobody to ask asks no allow question.** In a chart run that goes with the recommendations (intake's explicit launch, or a prompt or a yes that says so) the chart skill does not ask to allow `jig`, `git` and the oracles: the person's permission settings decide, a command they refuse hands its ticket back, and the run's report names it. A run without the recommendations (a confirmed launch whose yes did not say so, or a typed "Take <chart> to PR") asks it once, as before. jig sets no permission. The chart skill says what makes a run's prompt: one aimed at a chart, a yes to intake's confirm question, or intake's explicit launch; one ticket alone is not a chart run.

**A named existing ticket is a chart entry with its own id.** When an existing ticket's grilling ends in a chart, intake enters that ticket in `tickets.yaml` as an entry with its `id`, and a `title` and `body` scoped to the part of the work it names. `jig graduate` already keeps such an entry (`existing`), requires the ticket and a title, and never renames, closes or re-bodies the ticket, so nothing is closed or duplicated and the other entries carry the rest. The confirm question can then offer that ticket alone or the whole chart. The ticket's own `body` is not rewritten; its brief, drafted by the chart run, narrows it. "Take SHOP-1 to PR, go with your recommendations" for an existing SHOP-1 is a ticket run with nobody to ask; it meets the chart question only if SHOP-1's grilling ends in a chart, where it confirms.

## Rejected alternatives

- **Ask after every chart verdict.** The old rule: it asks a person who asked for no launch, and it stops a run the person left unattended on purpose.
- **Read "fix this" as asking for the result.** It names no phase past the brief, so a ticket run already stops after the brief and relays "Build T". Reading it as a launch would put it among the prompts that confirm, and a single ticket would then ask "build it now?" after every brief instead of stopping.
- **Code that classifies prompts.** ADR 0035 puts the reading in the session; a phrase list in Go would misread the next phrasing. The skills give examples and the session judges.
- **Close or rewrite the named ticket.** It would change a ticket outside the chart's own flow; keeping its id as an entry reuses what `jig graduate` already does.

## Consequences

- Supersedes [ADR 0037](0037-a-chart-is-run-from-the-main-session.md)'s decision "A chart verdict ends a ticket run, and the session asks": the verdict still ends the run, and what follows is the three outcomes above. Amends [ADR 0035](0035-start-to-finish-and-recommendations-are-separate.md): the intent to launch is one more thing the session reads from the prompt.
- This is skill text and the project stub, as the rule it replaces was: `skills/intake`, `skills/chart`, `skills/here` and `StubBody`. `jig graduate`, `jig next` and the chart's derived standing are unchanged.
- `CONTEXT.md` says what follows a verdict in *Start-to-finish run* and *Chart run*.

## A prompt that names some of a chart's tickets

A prompt that names several tickets of one chart ("Take A and B to PR", "Continue A and B") is a chart run over those tickets only. Before this, the chart run took the whole chart, so naming two tickets built them all.

### Decision

- **One derivation, filtered at its input.** `phase.ChartFacts` has `Named`, the ids the person named; empty means the whole chart. `phase.DeriveChart` works over the named entries only, so the rows, waves, briefs, ends, verdict and prompt come from one place, and `jig next` and `jig status` cannot disagree. The workspace overlaps and the worktrees read the run and the rows, so they follow.
- **`jig next <chart> --tickets A,B`** reads the chart this way: ids or aliases, any order, a repeat counted once. A name that is not a graduated ticket of the chart is refused, every offender named, with the chart's tickets in the help. `jig status <chart>` has no flag: it is the whole chart's standing.
- **A named ticket behind an unnamed one is an end.** An unmet need on an unnamed ticket that is not done takes `CHART_BLOCKER_NOT_NAMED`: the run does not build that ticket, so the named one is after-merge whatever the edge's kind, and the end names the blocker. When it is all the run waits for, it is the verdict's code, with the help "Name A too, or take A first", since no merge frees it. Beside a merge wait the verdict is `CHART_WAITS_MERGE`. A done unnamed blocker keeps today's rules.
- **The prompt names the tickets.** `TakeTicketsPrompt` and `ContinueTicketsPrompt` word it in row order. One named ticket is a ticket run, so it gets the ticket's own prompt. The echo is `named`, in JSON and as a pair of the `next` block.
- **No record.** Nothing stores the named set (ADR 0033, decision 4): the prompt carries it, and "Continue A and B" is read again with the same `--tickets`.
- The chart skill reads such a prompt (one clause, the chart found in the `charts` table of `jig next`) and intake's report lists it among the prompts that launch. Its confirmation follows the same intent reading as the whole chart's.

### Rejected alternatives

- **A filter over the output in `cmd/jig`.** The waves, ends and overlaps would come from two derivations that can disagree.
- **A recorded run or a named run object.** The prompt is the span of a run (decision 4).
- **A Go parser of prompts to find the tickets.** ADR 0035: the session reads the words and passes the ids.
- **Building the unnamed blocker in the run.** The person did not ask for it.

### Consequences

- A person who names two tickets gets two tickets' work and a wait for the rest. Resuming needs the same names.
- Naming every ticket of the chart derives as the whole chart, with the prompt naming the tickets as they were named.
- A stacked edge from a named ticket to a published unnamed one that is still unmet reads as a stacked parent outside the run (blocked), as a parent outside the chart does.
