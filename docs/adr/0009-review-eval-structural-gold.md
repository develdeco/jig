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
function, `Fold` and `FoldBefore`, wrapping - not replacing - the four
unexported helpers `gate.go` used to wire together inline
(`cumulativeFindings`, `openAndNotedFindingsList`, `dismissedFindingsList`,
`toDismissedFindingList`). `Gate` itself now calls `FoldBefore`, so a
corpus run and a real gate round can never disagree. The eval stops short
of routing: a round's fate is exactly `ApplyRound`'s own status assignment,
what a fresh finding carries before any human, or `--yes`, decides it,
since folding jig's default triage into a review-quality measurement would
score the repo's config, not the reviewer.

Nothing a live dispatch reads or is identified by may name the case under
measurement, or even that this is a case at all. Every case gets an opaque
run id, `runID(name)` - `c-` plus the first 8 hex characters of a sha256 of
the name - and that id, never the name, is what the store ticket,
`RoundInput.Ticket` (embedded verbatim in the reviewer's prompt), the
judge's dispatch ticket, and every work-root path are named after; the
case repo's own commit messages are neutral literals ("base", "round N"),
not named after the id either. `CaseScore.Name`, `JudgeQuery.Case` and
every error message still carry the real name; only a live dispatch may
not. The eval repo's own git identity (`runner.go`'s `identityEnv`) and
its store-side `project.yaml` are neutral for the same reason - a live
session can run `git log` in its worktree, so nothing there may say
"fixture" any more than it may say the case's name; the base commit itself
carries a neutral `go.mod` (`module example.com/project`, `go 1.22`), so
every case repo actually builds and a live reviewer's own `go test ./...`
can run, whatever a round's diff touches. A dedicated test,
`TestRunCaseNeverLeaksTheCorpusVocabulary` (`leak_test.go`), runs every
corpus case through a capturing backend and a capturing judge and checks
every surface a session with that dispatch could read - the prompt, the
dispatch paths, the dispatched slice file, every worktree file tracked or
not, the store's own ticket-dir files, and the worktree's git log - against
both the case's own name and a fixed vocabulary this package must never
use to describe itself (`eval`, `revieweval`, `fixture`, `trap`, `gold`,
`seeded`), tokenized without regexp (`strings.FieldsFunc`).

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
`prior` names a fold point in the same file - open, asked, noted or
dismissed - is a candidate for it whatever its line, line 0 included -
citing an id is itself location evidence - surviving on anything but an
explicit judge Different, without the extra burden of an explicit Same a
line-0 finding otherwise needs. A prior naming a point in a different
file, or one the judge rejects, is simply no edge, so the wrong-prior
rule below catches it like any other unsupported citation.

Seeded gold findings are matched to candidates one to one by an exact
maximum-weight assignment (`bestMatching`, the Hungarian algorithm), not
an order-biased heuristic, and it stays polynomial however many seeded
findings a captured case brings. Two matchings are compared by a
lexicographic tuple, most significant first: how many gold entries a
matching covers, how many edges the judge confirmed Same, how many cite
the gold's own prior, and the summed closeness of each matched line to
its span - never a finding's status or action, which the score itself
measures, so ranking the pairing by them would let the score choose its
own inputs. Only when two matchings tie on all of that does the summed
earliness of the matched findings decide, toward the earlier-reported
ones; the rare case where even that ties (more than one matching uses the
same set of findings) falls to the assignment algorithm's own row order,
deterministic for a given input but not itself a ranking field - the same
result every run, and a tie only the evidence itself cannot break, which a
live judge normally would (an explicit Same or Different resolves most of
what would otherwise tie). Every finding the match leaves over is then
classified
by the first of these rules that applies (`classifyUnmatched`): a
finding whose prior names any fold point - open, asked, noted or
dismissed - with no surviving edge to it is a wrong prior that fails the
round, since jig would move that point's identity onto code it is not
about; a well-cited repeat of a dismissed point is a permitted repeat
jig keeps dismissed, and counts nowhere; a well-cited repeat of any
other point goes on through the rules like any finding, so citing a
prior never excuses a false alarm; then a note; an edge to a fold finding already dismissed (re-litigated); an
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
label a pending one from the report alone. With no judge, only gold
matches are marked unconfirmed: a false alarm, a re-litigation or a lost
finding rests on structure alone just the same and is not marked, so a
run with no judge is a structural check. The live path always runs the
model judge.

The five cases ported from the earlier corpus kept their code, but
`clean`, `loopvar-trap` and `tenant-leak` lost sentences that gave the
reviewer the verdict: a brief saying the diff is correct, a brief
explaining the rule the trap tests, and a comment arguing the trap is
safe. A case must not tell the reviewer what it is testing, so this is a
deliberate content change, not only a translation to the new gold shape.

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
free-running drift is left for later. A round's verdict has three
values. It is FAIL when it is refused or failed, or when `Missed`,
`Lost`, `DroppedQuestions`, `Misattributed`, `FalseAlarms`,
`Relitigated` or `WrongPriors` is not empty. Otherwise it is PROVISIONAL
while any finding is still pending a label - a round cannot read as a
pass while findings nobody has judged stand in it - and PASS once none
is. A case takes its worst round's verdict. `Unconfirmed`, prior
citation, action agreement and triage prompts are measurements, never
part of the verdict.

CI runs the structural path with a scripted reviewer backend, so the
scorer has to prove it can fail as well as pass. `perfect`, `regressed`
and `refused` run with no judge, since each tests structural matching on
its own; `gamed` also runs with a scripted judge, proving the judge stage
itself, not the window, rejects a gamed finding. `perfect` passes every
round of every case; `regressed` fails each case in exactly one designed
way - a missed bug, a flagged trap, a lumped finding, an exhaustive round's
leftover false alarm (`clean`), a forgotten finding, a re-litigated
dismissal, a matched finding scored on the wrong prior, a lost fix, a
dropped question - never a bare wrong prior, since a matched finding
scores against its gold entry regardless of what it cited;
`probes` adds cases exercising the paths the standing corpus does not
otherwise reach, among them an unmatched finding whose cited prior is
never confirmed. `gamed` puts a finding at the seeded line arguing the
wrong thing: the scripted judge answering `different` misses it, and no
judge matches it but lists it unconfirmed, showing the judge, not the
window, rejects it. `refused` leaves a `must_review` path out of
`reviewed_paths`, so its gold counts as missed.

The live path (`live_test.go`, gated on `JIG_REVIEWEVAL_BACKEND`) is
report-only - a measurement, not a build gate - except a round that comes
back Failed, which fails the test: a dispatch failure, a judge error, the
judge changing the case repo, or a reviewer that wrote no `result.json`
at all. That last one is the reviewer's own behavior rather than
infrastructure, but it gives the eval nothing to score, so it is surfaced
the same way. A failed round's gold counts as missed, never left out of
the denominator; a refused round still only measures, and a judge error
still leaves that round's findings in `RenderJSON`. The live path
controls what its sessions see: each child gets an environment built
from the test process's own minus every `JIG_` variable, the git test
scaffolding, launch-context entries and any stale `PWD`
(`session.Options.Env`, which a backend that cannot apply it refuses),
and the judge's scratch lives in its own temp root outside the case work
root. A test drives a case through the real headless backend with a
stub CLI and checks the environment, working directory and surroundings
every child actually saw. Its parent environment is one planted value of
every kind the scrub must drop, plus every variable the test harness
itself added after launch, so the check is strict. What the operator
brings is ambient and outside what the eval scrubs: the variables of the
shell that launched the run pass through by design, and every path sits
under the operator's temp root. The leak checks leave both out, and run
under a temp root deliberately named with a leak word, so they hold on
any host. The live number still assumes a reviewer that does not go
looking: the corpus and its `gold.yaml` sit on the same disk, and the
headless backend is not a read boundary. A judge dispatch is held to the
reviewer's own read-only rule: after every round the runner checks the case repo's HEAD
and tracked files against that round's head and restores the repo
regardless, keeping a git command failing during that check apart from an
actual violation ("the judge changed the case repo"). Once scored, the
runner deletes - never moves or archives - the round's store-side work
dir and judge scratch dir (and the judge's temp root when the case
ends), so no later round's own dispatch, reading only
what the store and the work root currently hold, can find an earlier
round's live result disagreeing with the case's recorded history; this
claim is scoped to that - a later session free to read wherever it likes
could still find the standing report on disk, or the CLI's own session
transcripts, neither of which this deletion touches. Cases run one at a
time through `RunCase`, rewriting the report and JSON after each one, so
`-timeout 0` in the documented command keeps whatever finished on disk if
a long run times out or panics partway through the corpus. The report
always lands on disk, and never under the work root the run itself
deletes: unset, `JIG_REVIEWEVAL_REPORT` falls back to `defaultReportDir()`
(`os.UserCacheDir()/jig/revieweval`, falling back to `os.TempDir()` only
when `UserCacheDir` itself fails), never `t.TempDir()` (which the test
removes the moment it ends) and never the work root (`os.MkdirTemp("",
"jig-")`, also removed at the end - not `t.TempDir()`, whose own directory
is named after the running test), logged once at the start; each case's
own lines log as it finishes, the cumulative report once more after the
loop.

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
