# demo/adopt-branch.tape
#
# Records the hand-written-branch loop: a branch built outside jig and
# pushed, a ticket minted for it, `jig gate --branch` reviewing it and
# finding a fix, `jig run` building that fix ON the author's branch (a
# `git log` of the build lease shows the fix commit sitting on top of the
# author's commits, which origin has not changed), the next `jig gate`
# reviewing the branch again and coming back clean, a `jig status` that
# suggests `jig publish`, `jig publish` shipping the branch as it is, and a
# `git log` of origin's branch: the author's commits unchanged, jig's fix and
# publish's memorize commit on top. This is the loop ADR 0013
# (docs/adr/0013-a-ticket-branch-is-recorded.md) exists to make work: before
# it, the fix was built on a jig/<ticket> branch cut from main, which lacks
# the author's code, and the next gate reviewed that branch instead.
#
# Every typed line here is checked against the real source, the same as
# gate-reviewer.tape's own header states: the reports below are checked
# against cmd/jig/gate.go (printGateReport's kv fields, rendered via
# axi.KV, with the branch line an adopted ticket adds), cmd/jig/status.go
# (RenderStatus and nextStepHint) and cmd/jig/publish.go (pushedTable).
# e2e/adopt_branch_test.go
# (TestGateBranchRoundFixesBuildOnTheReviewedBranch) drives the same
# commands against the built binary with a hand-written branch, but it checks
# the outcome (where the fix commit landed, what the ticket recorded), never
# this tape's own typed lines - see demo/README.md for that caveat.
#
# Nothing on camera waits on an interactive prompt: the scripted source
# (testdata/fixture/scenario/gate/round-1) raises a fix without ever
# dispatching a reviewer, round 1's fix-1 has a canned attempt in the same
# base scenario so the `jig run` finishes unattended, and round 2 has no
# gate/round-2 directory to play back at all. So no triage prompt appears
# anywhere on camera, and every Wait below is a bare Wait (VHS's own default
# prompt and pattern), never a Wait+Line anchored on a prompt's own trailing
# text.
#
# The author's branch is built from the fixture's own scenario patches (slice
# a's Clamp fix, then slice b's ClampPercent and its fix), so it passes the
# fixture repo's oracles the way a finished branch would, and the fix the
# scripted round raises (a doc comment on Add) applies on top of it.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME,
# the store, the scenario, and the fixture repo's remote all live under a
# fixture this tape builds for itself (demo/fixture, testdata/fixture/scenario
# - the base scenario, no scenario-branches overlay), and every command below
# names them only by env var ($DEMO_DIR, $JIG_HOME, $JIG_STORE_DIR,
# $JIG_SCENARIO_DIR) - never an expanded path. The fixture's own ticket is
# JIG-1; the ticket minted here is JIG-2, which the fixture's local tracker
# numbers next.

Output demo/out/adopt-branch.gif
Output demo/out/adopt-branch.mp4

Require jig
Require fixture
Require git

# Same grid as gate-reviewer.tape, for the same reason (see its own
# comment): `Set Columns` pins the count directly rather than leaving it
# to the runner's installed fonts, so the trimmed tail of the cursor's row
# is the same on every runner. No row this tape prints is wider than
# 160 columns, and the prompt never fills a row.
Set Shell "bash"
Set FontSize 14
Set Columns 160
Set Height 800
Set Padding 20
Set TypingSpeed 30ms
Set Framerate 30
Set CursorBlink false

Hide

# Build the fixture fresh, into its own scratch dir, and load its paths as
# env vars - after this line, no command ever types a literal path again.
# JIG_HOME is exported before the generator ever runs: VHS starts bash with
# the host's own environment appended, so a developer who already has a
# real JIG_HOME exported must never have it inherited here.
# -scenario "" builds the base scenario tree with no scenario-branches
# overlay (fixture.Opts{}'s own default), the same one
# TestGateBranchRoundFixesBuildOnTheReviewedBranch builds.
Type `export DEMO_DIR="$(mktemp -d)"` Enter
Wait@5s
Type `export JIG_HOME="$DEMO_DIR/home"` Enter
Wait@5s
# `git log` on a terminal opens a pager unless its output fits the screen; a
# pager that waits for a key would hold the tape at its next Wait forever.
Type `export GIT_PAGER=cat` Enter
Wait@5s
Type `eval "$(fixture -out "$DEMO_DIR" -scenario "")"` Enter
Wait@30s

# The author's side: a clone of the fixture repo's remote, a branch off main
# with three commits built from the scenario's own patches, pushed. The
# commits are the author's alone - jig has never seen this branch. The
# identity is set on the clone itself, so nothing depends on the host's git
# config.
Type `git clone -q "$DEMO_DIR/fixture-repo.git" "$DEMO_DIR/author"` Enter
Wait@30s
Type `cd "$DEMO_DIR/author"` Enter
Wait@5s
Type `git config user.name Author && git config user.email author@example.invalid` Enter
Wait@5s
Type `git checkout -q -b add-percent` Enter
Wait@5s
Type `git apply "$JIG_SCENARIO_DIR/slices/a/attempt-1/patch.diff" && git commit -qam "Clamp: honor the upper bound"` Enter
Wait@10s
Type `git apply "$JIG_SCENARIO_DIR/slices/b/attempt-1/patch.diff" && git add -A && git commit -qm "Add ClampPercent"` Enter
Wait@10s
Type `git apply "$JIG_SCENARIO_DIR/slices/b/attempt-2/patch.diff" && git commit -qam "ClampPercent: cap at 100"` Enter
Wait@10s
Type `git push -q origin add-percent` Enter
Wait@30s

# A neutral working directory: nothing below depends on the shell's own
# cwd, but the prompt (and any relative-path slip) must never show the
# fixture's own tree.
Type `cd "$HOME"` Enter
Wait@5s

# Wipe the hidden setup's own commands and output before the recording
# starts, so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## The branch, as its author left it on origin: three commits on top of the
## fixture repo's initial one.
Type `git -C "$DEMO_DIR/fixture-repo.git" log --oneline add-percent` Enter
Wait@5s
Sleep 6s

## A ticket for it, minted with no brief and no slices: "ticket:\n  id:
## JIG-2\n  title: Add ClampPercent", and the two ways to give it work - a
## brief with slices, or a branch built outside jig, which is this tape's
## (cmd/jig/ticket.go's cmdTicket).
Type `jig ticket new --title "Add ClampPercent" --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 6s

## Round 1: `--branch` names the branch the ticket adopts. The scripted round-1
## raises one fix without dispatching a reviewer, so there is no prompt to
## wait on - the report prints as soon as the command finishes. Brief-less,
## with no intent.md, and with no reviewer to infer one from the author's
## session (inference is reviewer-source code, internal/verifydeliver/review.go,
## which a reviewer round on an adopted branch runs like any brief-less
## ticket's), the round has no intent: "gate:\n  ticket: JIG-2\n  branch:
## add-percent\n  round: 1\n  verdict: fix-slices\n  model: claude-sonnet-5\n
## intent: none\n..." (axi.KV, cmd/jig/gate.go's printGateReport). The gate ran
## the oracles over origin's add-percent.
Type `jig gate JIG-2 --branch add-percent --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@90s
Sleep 6s

## Build the fix the round queued (fix-1). It is built in jig's own lease of
## the branch, on the author's commits: "run:\n  ticket: JIG-2\ngreen[1]{id}:
## fix-1..." (cmd/jig/run.go's printRunReport).
Type `jig run JIG-2 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 4s

## Where the fix commit went: the top of add-percent in jig's build lease,
## directly on the author's three commits, whose tip is still origin's
## add-percent. jig has pushed nothing; its commit waits in the lease.
Type `git -C "$JIG_HOME/pool/fixture-repo/JIG-2" log --oneline --decorate -5` Enter
Wait@5s
Sleep 8s

## Round 2 names no branch: the ticket recorded it. The scripted source has no
## gate/round-2 directory, so it plays back nothing and the round is clean:
## "gate:\n  ticket: JIG-2\n  branch: add-percent\n  round: 2\n  verdict:
## clean\n  ..." - reviewing add-percent as jig's lease now holds it, fix
## commit included.
Type `jig gate JIG-2 --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@90s
Sleep 6s

## The ticket's final state: the branch it adopted, every slice green, and
## the next step after a clean round, which is `jig publish` like any
## ticket's ("help[1]:\n  Run `jig publish JIG-2` to open or update the PR",
## cmd/jig/status.go's nextStepHint).
Type `jig status JIG-2 --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 6s

Type "clear" Enter
Wait@5s

## Publish ships the branch as it is (verifydeliver/publish.go). The branch is
## already on origin, so nothing is squashed: the pushed table says "not
## squashed (branch already on origin)" beside the head that was pushed
## (cmd/jig/publish.go's pushedTable). --yes stands in for the confirm
## question, so nothing waits on a prompt; the fixture's tracker is local, so
## no pull request is opened, and pr_url is empty.
Type `jig publish JIG-2 --yes --store "$JIG_STORE_DIR"` Enter
Wait@120s
Sleep 8s

## Origin's add-percent now: the author's three commits, listed at the top of
## this recording, are unchanged - same shas, same order - with jig's fix
## commit ("JIG-2 fix-1: ...") and publish's memorize commit ("docs: memorize
## JIG-2") on top. Nothing was rewritten: origin's branch was fast-forwarded.
Type `git -C "$DEMO_DIR/fixture-repo.git" log --oneline add-percent` Enter
Wait@5s
Sleep 12s
