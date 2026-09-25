# Review eval: gold is structural, not a pattern over prose

`internal/revieweval` measures the gate reviewer's own quality: whether a
real review round, run through the real code path, finds the problems a
case seeded on purpose and stays quiet about the code that is actually
fine. A round dispatches through the same call Gate itself makes,
`verifydeliver.NewReviewerGateSource(backend).Round`, applies the same
`verifydeliver.ApplyRound` and `verifydeliver.ClearingAfterTriage` that
decide a finding's fate on a real ticket, and reads a round's starting
history from the store's own `gate/round-N/findings.yaml`, folded the same
way Gate folds it: `findings.go` exports that fold as one type and one
function, `Fold` and `FoldBefore` - three unexported helpers wired
together inline in `gate.go` before - and `Gate` itself now calls
`FoldBefore`, so a corpus run and a real gate round can never disagree.
The eval stops short of routing: a round's fate is exactly `ApplyRound`'s
own status assignment, what a fresh finding carries before any human, or
`--yes`, decides it, since folding jig's default triage into a
review-quality measurement would score the repo's config, not the
reviewer.

Nothing a live dispatch reads or is identified by may name the case under
measurement. Every case gets an opaque run id, `runID(name)` - `c-` plus
the first 8 hex characters of a sha256 of the name - and that id, never
the name, is what the store ticket, `RoundInput.Ticket` (embedded
verbatim in the reviewer's prompt), the judge's dispatch ticket, every
work-root path, and the case repo's commit messages are named after.
`CaseScore.Name`, `JudgeQuery.Case` and every error message still carry
the real name; only a live dispatch may not.

Matching what a round reports against what a case seeds is structural,
never a pattern over the reviewer's prose, which reads a differently
worded real problem as a miss and a negated finding using the pattern's
own words as a hit. `match.go` asks only whether a reported finding names
the same file as a gold point (a seeded finding, a trap, a recorded
decision, or a fold finding already dismissed) and sits within
`lineWindow` (3) lines of that point's span; a survivor is a structural
candidate, not yet a match. A dismissed fold point's span and description
ordinarily come from its one recorded line and title and detail; when a
decision names it (`decisions.yaml`'s optional `recorded: <id>`) and that
decision's file still matches the fold's current one, the point instead
takes the union of the decision's own span with that record's line as the
loader captured it (`Decision.RecordedLine`), described by the decision
itself, or the bare record on a file mismatch - never the fold's latest
occurrence, which a re-report could have moved. The loader bounds every
link at load time: it rejects a `recorded` line outside the decision's own
span widened by `lineWindow`, and a second decision anywhere in the case
naming a record another decision already claimed. A finding whose own
`prior` names a dismissed fold point in the same file is a candidate for
it whatever its line, line 0 included - citing an id is itself location
evidence - surviving on anything but an explicit judge Different, without
the extra burden of an explicit Same a line-0 finding otherwise needs. A
prior naming a point in a different file, or one the judge rejects, is
simply no edge, so the wrong-prior rule below catches it like any other
unsupported citation.

Seeded gold findings are matched to candidates one to one by an exact
maximum-weight assignment (`bestMatching`, the Hungarian algorithm), not
an order-biased heuristic, and it stays polynomial however many seeded
findings a captured case brings. Two matchings are compared by a
lexicographic tuple, most significant first: how many gold entries a
matching covers, how many edges the judge confirmed Same, how many cite
the gold's own prior, and the summed closeness of each matched line to
its span - never a finding's status or action, which the score itself
measures, so ranking the pairing by them would let the score choose its
own inputs. Only when two matchings tie on all of that does report order
decide, toward the earlier-reported findings, so the result is the same
every run. Every finding the match leaves over is then classified
by one of seven rules in order (`classifyUnmatched`): jig's status is
dismissed with a surviving edge to the dismissed point its prior names -
a permitted repeat - or without one, a wrong prior that fails the round;
a note; an edge to a fold finding already dismissed (re-litigated); an
edge to a trap or a dismissed decision (a false alarm); an edge to a kept
decision (a true positive beyond gold); an exhaustive round's leftover
(also a false alarm); or otherwise pending - run only on a finding the
matching above left unpaired.

Structure alone cannot decide every candidate: more than one point can
share a finding's window, and a line-0 finding carries no location
evidence of its own, unless it is that finding's own cited prior (above).
A judge decides those - the `Judge` interface, asked once per round with
the whole batch, the same question for every candidate whatever kind of
point it pairs a finding with, and told neither which kind that is nor
which case this round belongs to: does the finding raise the same problem
as the point's description. `judge.go`'s `ModelJudge` is the live judge:
it writes every candidate to `judge.json`, dispatches one screened session
under the case's opaque ticket, and reads `verdicts.json` back strictly,
one verdict per candidate, no unknown, missing, null or duplicate keys. A
nil judge answers Undecided, which an ordinary edge survives but a plain
line-0 finding never does. A round's `Unconfirmed` gold, found on
structure alone, is a count in `RenderReport`; `RenderJSON` carries every
reported finding with jig's status and either the gold id it matched or
how it was classified, so a person can confirm an unconfirmed one or
label a pending one from the report alone.

Seeded gold is written into a case when the case is built, not discovered
by eyeballing a review afterward - `gold.yaml`'s findings and traps are
the problems, and the tempting-but-correct code, `patch.diff` seeded on
purpose. Findings beyond that gold are labeled from recorded human
decisions, `decisions.yaml`: `kept` is a true positive, `dismissed` a
false positive, undecided stays pending - the same vocabulary a real
ticket already writes. Multi-round cases are teacher-forced: round N's
fold always comes from the case's own recorded
`round-(N-1)/findings.yaml`, copied into the eval store before round N
runs, never from what this run's reviewer said in round N-1, so a round
that drifts off course cannot drag a later round down or inflate it, and
free-running drift is left for later. A round passes when it is neither
refused nor failed and `Missed`, `Lost`, `DroppedQuestions`,
`Misattributed`, `FalseAlarms`, `Relitigated` and `WrongPriors` are all
empty; `Unconfirmed`, `Pending`, prior citation, action agreement and
triage prompts are counted but never fail a round - what the eval cannot
resolve on its own, not evidence the reviewer did anything wrong.

CI runs the structural path with a scripted reviewer backend, so the
scorer has to prove it can fail as well as pass. `perfect`, `regressed`
and `refused` run with no judge, since each tests structural matching on
its own; `gamed` also runs with a scripted judge, proving the judge stage
itself, not the window, rejects a gamed finding. `perfect` passes every
round of every case; `regressed` fails each case in exactly one designed
way - a missed bug, a flagged trap, a lumped finding, a forgotten finding,
a re-litigated dismissal, a matched finding scored on the wrong prior, a
lost fix, a dropped question - never a bare wrong prior, since a matched
finding scores against its gold entry regardless of what it cited;
`probes` adds cases exercising the paths the standing corpus does not
otherwise reach, among them an unmatched finding whose cited prior is
never confirmed. `gamed` puts a finding at the seeded line arguing the
wrong thing: the scripted judge answering `different` misses it, and no
judge matches it but lists it unconfirmed, showing the judge, not the
window, rejects it. `refused` leaves a `must_review` path out of
`reviewed_paths`, so its gold counts as missed.

The live path (`live_test.go`, gated on `JIG_REVIEWEVAL_BACKEND`) is
report-only - a measurement, not a build gate - except a round that comes
back Failed (a dispatch failure, a judge error, or the judge changing the
case repo), which is infrastructure trouble and fails the test; a refused
round still only measures, and a judge error still leaves that round's
findings in `RenderJSON`. A judge dispatch is held to the reviewer's own
read-only rule: after every round the runner checks the case repo's HEAD
and tracked files against that round's head and restores the repo
regardless, keeping a git command failing during that check apart from an
actual violation ("the judge changed the case repo"). Once scored, the
runner deletes - never moves or archives - the round's store-side work
dir and judge scratch dir, so no later dispatch can find a live result
disagreeing with the case's recorded history. Cases run one at a time
through `RunCase`, rewriting the report and JSON after each one, so
`-timeout 0` in the documented command keeps whatever finished on disk if
a long run times out or panics partway through the corpus. The report
always lands on disk: unset, `JIG_REVIEWEVAL_REPORT` falls back to a
stable path under `os.TempDir()` (`report.txt` and `report.txt.json`
under a `jig-revieweval` directory) rather than `t.TempDir()`, logged once
at the start; each case's own lines log as it finishes, the cumulative
report once more after the loop.

The eval repo's per-round git identity and commit date are fixed, so two
runs of the same corpus produce comparable shas. The default model is
derived, not hardcoded: the rung after the cheapest, the pick `Gate`
itself makes once an unattended ticket's builders have used the cheapest
rung - the earlier default returned the cheapest rung itself, only what
`Gate` picks before any builder has dispatched, the wrong default once a
ticket has slices.

Deferred, each recorded so later work does not have to re-derive why:

- **Capturing cases from real tickets into a corpus.** Today's cases are
  hand-built; growing the corpus from what a real reviewer actually missed
  or invented on a real ticket is its own change, and probably belongs
  outside this repo rather than in it.
- **Free-running multi-round drift**, where a case's later round starts
  from what an earlier live round actually reported rather than the
  case's recorded history. Teacher-forcing measures each round's own
  behavior in isolation; it says nothing about whether a reviewer's own
  mistake compounds across rounds.
- **Triage that adds a missed finding.** A human at the triage seam can
  only keep or dismiss what the reviewer reported; there is no way for a
  person's own catch, something the reviewer missed outright, to become a
  recorded decision this eval could later score. Recovering a miss this
  way is a real thing a good review process could do; jig's triage does
  not do it yet.
