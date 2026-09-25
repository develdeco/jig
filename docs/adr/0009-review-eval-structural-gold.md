# Review eval: gold is structural, not a pattern over prose

`internal/revieweval` measures the gate reviewer's own quality: whether a
real review round, run through the real code path, finds the problems a
case seeded on purpose and stays quiet about the code that is actually
fine. It never substitutes a stand-in for any part of that path. A round
dispatches through the same call Gate itself makes,
`verifydeliver.NewReviewerGateSource(backend).Round`, applies the same
`verifydeliver.ApplyRound` and `verifydeliver.ClearingAfterTriage` that
decide a finding's fate on a real ticket, and reads a round's starting
history from the store's own `gate/round-N/findings.yaml`, folded the same
way Gate folds it. That fold used to be three unexported helpers wired
together inline in `gate.go`; `findings.go` now exports it once, as `Fold`
and `FoldBefore`, and `Gate` calls `FoldBefore` instead of assembling the
three helpers itself, so a corpus run and a real gate round can never
quietly start a round from two different ideas of its history.

The eval stops short of one thing Gate itself does next: routing. A
round's fate is exactly `ApplyRound`'s own status assignment - the same
status a freshly reported finding carries before any human decides it, or
before `--yes` decides it for them - because folding jig's own
default-triage policy (which findings to auto-approve, which fixes to
queue) into a review-quality measurement would score the repo's triage
defaults, not the reviewer that actually read the diff.

Matching what a round reports against what a case seeds is structural,
never a pattern over the reviewer's prose. A pattern fails in both
directions at once: a review that finds the real problem but words it
differently than the pattern expects reads as a miss, and a review that
uses the pattern's own words while arguing the code is actually fine - a
negated finding - reads as a hit. `match.go` instead asks only whether a
reported finding names the same file as a gold point (a seeded finding, a
trap, a recorded decision, or a fold finding already recorded as
dismissed) and sits within `lineWindow` (3) lines of that point's seeded
span; a survivor there is a structural candidate, not yet a match. A
dismissed fold point's span and description ordinarily come from its one
recorded line and its recorded title and detail; when a decision names it (`decisions.yaml`'s
optional `recorded: <id>`), the point instead takes the union of that
line and the decision's own span, described by the decision itself, since
the person who dismissed it judged more than the single line the store
kept. Seeded gold findings are matched to candidates one to one by a
maximum bipartite matching over a round's surviving edges (Kuhn's
augmenting-path algorithm, so a finding that lumps two real problems into
one cannot starve a second gold entry it could just as well have
satisfied instead of the first); among the matchings the algorithm could
return, findings are augmented in order of their own best-supported edge
(the judge said Same, then the finding's status agrees with the gold
action, then its line sits inside the span itself, then result order), so
which finding claims a gold entry two of them could both satisfy is never
an accident of report order. Every finding the match leaves over is then
classified by one of seven rules in order (`classifyUnmatched`): jig's own
status is dismissed with a surviving edge to the dismissed point its
prior actually names - a permitted repeat - or without one, a wrong prior
that fails the round; a note; an edge to a fold finding already dismissed
(re-litigated); an edge to a trap or a dismissed decision (a false
alarm); an edge to a kept decision (a true positive beyond the seeded
gold); an exhaustive round's leftover (also a false alarm); or otherwise
pending.

Structure alone cannot decide every candidate: more than one point can
share a finding's window (a trap sitting right beside the seeded bug,
say), and a finding with no line at all (line 0) carries no location
evidence of its own to accept on structure alone. What decides those is a
judge - the `Judge` interface, asked once per round with that round's
whole batch of candidates, the same single question for every one of
them regardless of which kind of point it pairs a finding with: does the
finding raise the same problem as the point's description. The judge is
never told which kind a point is - a seeded finding, a trap, a decision,
or a dismissed record - so it has no shortcut but that description, which
`match.go` always states the way a finding would raise the point (the
real problem, the wrong concern a trap tempts, or the point a decision or
a dismissal actually settled). `judge.go`'s `ModelJudge` is the live
path's judge: it writes every candidate's point and finding to
`judge.json`, dispatches one screened session, and reads `verdicts.json`
back strictly, one verdict per candidate, unknown keys and missing, null
or duplicate candidates all rejected. A nil judge answers every candidate
Undecided, which an ordinary edge survives - structure alone is enough to
propose it - but a line-0 finding never does. A round's `Unconfirmed`
gold - found on structure alone, with no judge confirmation - is a count
in `RenderReport` and the actual list of ids in `RenderJSON`, for a person
to confirm rather than silently counted as found.

Seeded gold is not discovered after the fact by running a review and
eyeballing what came back; it is written into a case when the case is
built - `gold.yaml`'s findings and traps are the problems, and the
tempting-but-correct code, that the case's `patch.diff` seeded on
purpose. Findings a round reports beyond that seeded gold are labeled from
recorded human decisions, `decisions.yaml`: `kept` is a true positive,
`dismissed` is a false positive, and anything nobody decided stays
pending - exactly the vocabulary the reviewer's own triage seam already
writes to a real ticket's `findings.yaml` (`triage: human`, `status:
dismissed` or `status: open`/`asked`), under its own `decision: kept` or
`decision: dismissed` key - a different key from `findings.yaml`'s own
`decision` field, an ask's free-text human answer, not a kept/dismissed
verdict.

Multi-round cases are teacher-forced. Round N's fold always comes from the
case's own recorded `round-(N-1)/findings.yaml` - what the case says jig
actually recorded after that round's triage - copied into the eval store
before round N runs, never from what this run's own reviewer said in round
N-1; once a round is scored, the runner also moves everything its own
dispatches wrote under the store (`review.json`, `result.json`) out to the
run's own work dir, so a later round's reviewer or judge can never
stumble onto a live file sitting where the store would otherwise still
hold it instead of the case's recorded truth. Every round then tests
exactly the behavior it was built to test,
whatever an earlier round's live result happened to be; a round that
drifts off course cannot drag every later round in the case down with it,
and cannot inflate it either. Free-running drift, where round N actually
starts from round N-1's live result, is left for later.

A round passes when it is neither refused nor failed and `Missed`, `Lost`,
`DroppedQuestions`, `Misattributed`, `FalseAlarms`, `Relitigated` and
`WrongPriors` are all empty - the reviewer found every seeded problem, at
the labeled action, raised nothing that resolved false or restates
something already settled, and cited no dismissal it was not structurally
repeating. `Unconfirmed`, `Pending`, prior citation, action agreement and
triage prompts are counted but never fail a round: they are what the eval
cannot resolve on its own (a judge or a person still has to), not evidence
the reviewer did anything wrong.

CI runs the structural path over the real corpus with a scripted reviewer
backend in place of a model, so the scorer has to prove it can fail as
well as pass. `perfect`, `regressed` and `refused` run with no judge at
all, since each is built to test structural matching on its own; only
`gamed` also runs with a scripted judge, which is what proves the judge
stage itself, not the structural window, is what rejects a gamed finding.
The `perfect` fixture set passes every round of every case. The
`regressed` set fails each case in exactly one designed way, pinned as an
exact list per case: a missed bug, a flagged trap, a lumped finding, a fix
raised on a clean diff, a forgotten finding that coverage cleared, a
dismissed point raised again without its prior, a wrong-prior citation, a
behavior change reported as a note, and a judgment call labeled `fix`. The
`gamed` set puts a finding at the seeded line that argues the wrong thing:
with the scripted judge answering `different` it is missed, and with no
judge it matches but is listed as unconfirmed, which is what shows the
judge is the stage that rejects it. The `refused` set leaves a
`must_review` path out of `reviewed_paths`, and the round's gold counts as
missed.

`RenderJSON` carries every reported finding with jig's status for it and
either the gold id it matched or how it was classified, so a person can
label the pending ones from the report alone.

The live path (`live_test.go`, gated on `JIG_REVIEWEVAL_BACKEND`) is
report-only: a case's result is a measurement of review quality over
time, not a gate on the build - except a round that comes back Failed (a
dispatch failure, a judge error, or the judge changing the case repo) is
infrastructure trouble, not a measurement, and fails the test; a refused
round still only measures. A judge dispatch is held to the same
read-only rule as the reviewer's own: after every round the runner checks
the case repo's HEAD and tracked files against that round's own head, and
whatever it finds, restores the repo (`git reset --hard`, `git clean
-fd`) before the next round's patch applies. Cases run one at a time
through `RunCase`, rewriting the text report and JSON after every case,
so a long run (the documented command passes `-timeout 0`, disabling
Go's own default) still leaves whatever finished on disk if it times out
or panics partway through the corpus. Only an infrastructure failure
beyond a Failed round - the backend unavailable, a case that cannot even
be materialized, a corpus that fails to load - fails the test otherwise.

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
