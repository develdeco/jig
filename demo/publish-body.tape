# demo/publish-body.tape
#
# Records what `jig publish` leaves behind: a lean pull request body,
# `pr/<repo>.md` (up to four `## ` sections - Intent, What changed, Demo,
# Verification - and nothing else), and the full review detail in a separate
# file, `pr/review-notes.md` (every finding across every gate round, ordered
# by risk, with how it ended). This is the flow jig's publish exists to make
# true: a reviewer finds why the change exists, what changed, the change
# working - the Demo section, present here because the reviewer scenario's
# clean round 3 records one - and how it was verified, all in the body, and
# everything else in one comment - here, on the local tracker, a file in the
# store instead, since the local tracker posts nothing and uploads no media
# (see ARCHITECTURE.md#publish and
# README.md#what-a-published-pull-request-looks-like).
#
# The setup that reaches a clean, publishable round is hidden: it is the same
# three rounds (a fix kept, a second fix dismissed at the batch prompt, an
# ask kept with a decision, a note; a delta round where the kept fix recurs
# and the dismissed one reaches no one; a clean third round with its demo)
# that demo/gate-reviewer.tape records on camera, over the same `reviewer`
# fixture scenario - see that tape's own header for the full citation of
# every typed line and every anchored Wait+Line against cmd/jig/triage.go and
# cmd/jig/gate.go, and for the Columns/Wait+Line width proof this tape
# inherits unchanged (same prompts, same scenario, same 160-column grid).
# Hiding it here is only about what this recording shows, never about what
# runs: Hide stops VHS from rendering frames, not from typing into the real
# pty, so the hidden gate rounds below produce the same findings.yaml and
# slices.yaml a shown round would. cmd/jig/gate_reviewer_e2e_test.go
# (TestGateReviewerRoundsThroughMain) drives the identical scripted stdin
# through the same scenario and pins every finding's id, status and outcome
# this tape's own review-notes.md ends up with.
#
# What is shown is `jig status` (every slice green, ready to publish),
# `jig publish JIG-1 --yes` (the publish report: squashed, the PR body's
# store path, no pr_url row - the local tracker opens no pull request), and
# a `cat` of each file the brief asks for. Every row either command prints is
# checked against the real source: cmd/jig/status.go (RenderStatus's own
# `slices`/`questions` tables) and cmd/jig/publish.go (`pushedTable`,
# `pr_body` and `prURLTable`); the two files' content against
# internal/verifydeliver/render.go (`writePRBody`, `renderDemoSection`,
# `writeReviewNotes`). None of the three waits on an interactive prompt, so
# every Wait below is a bare Wait (VHS's own default prompt and pattern),
# never a Wait+Line.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME,
# the store, and the scenario all live under a fixture this tape builds for
# itself (demo/fixture, testdata/fixture/scenario-branches/reviewer), and
# every command below names them only by env var ($JIG_HOME, $JIG_STORE_DIR,
# $JIG_SCENARIO_DIR, $JIG_TICKET) - never an expanded path. "fixture-repo" and
# "JIG-1" are fixture data (the repo's own name and the fixture's own ticket
# id), not a host path, the same as every other tape types them.

Output demo/out/publish-body.gif
Output demo/out/publish-body.mp4

Require jig
Require fixture

# Same grid as gate-reviewer.tape, for the same reason (see its own
# comment): `Set Columns` pins the count directly rather than leaving it to
# the runner's installed fonts. The hidden triage prompts below are the
# same prompts at the same columns, already proven to clear every
# Wait+Line anchor with margin to spare; nothing this tape shows on camera
# anchors on a prompt at all (see header above), so no new width proof is
# needed for the shown half.
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
Type `export DEMO_DIR="$(mktemp -d)"` Enter
Wait@5s
Type `export JIG_HOME="$DEMO_DIR/home"` Enter
Wait@5s
Type `eval "$(fixture -out "$DEMO_DIR" -scenario reviewer)"` Enter
Wait@30s

# A neutral working directory: nothing below depends on the shell's own
# cwd, but the prompt (and any relative-path slip) must never show the
# fixture's own tree.
Type `cd "$HOME"` Enter
Wait@5s

# Slices a, b, d build straight through; c pauses on q-001 (run exits 2) -
# the same shape gate-reviewer.tape's own hidden setup builds.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Type `jig run "$JIG_TICKET" --answer q-001 "Casual." --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

## Round 1 (full), hidden: the real triage prompt, answered exactly as
## gate-reviewer.tape's own Show section types it - dismiss the low-risk
## trim fix (r1-f2), keep the high-risk overflow-doc fix (r1-f1) in the
## batch, keep the ask (r1-f3) with a decision. Ends "verdict: fix-slices",
## with fix_slices fix-1-alpha-test and fix-1-r1-f3 (the dismissed fix never
## gets one).
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait+Line@15s /good\):$/
Type "r1-f2" Enter
Wait+Line@15s /dismiss\]:$/
Type "k" Enter
Wait+Line@15s /skip\):$/
Type "Use a warm, casual tone; no exclamation marks." Enter
Wait@15s

## Build round 1's fix slices (fix-1-alpha-test, fix-1-r1-f3) to green.
Type `jig run JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

## Round 2 (delta), hidden: the doc-comment fix (r1-f1) recurs as this
## round's only fix - Enter alone accepts the batch of one. The tone ask
## already cleared and the trim fix's dismissal carried forward, so neither
## prompts again.
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait+Line@15s /good\):$/
Enter
Wait@15s

## Build round 2's fix slice (fix-2-alpha-test) to green.
Type `jig run JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

## Round 3, hidden: clean, with its demo (the scenario scripts one with two
## small SVG frames). Every slice is now green and the ticket has nothing
## left to decide.
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@30s

# Wipe the hidden setup's own commands and output before the recording
# starts, so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## Every slice green, nothing outstanding, and the next step is publish:
## "ticket: JIG-1\nstate: green\nslices[7]{id,state,attempts,blocked_by,
## question}:\n  a,green,...\n  ...\nhelp[1]:\n  Run `jig publish JIG-1`..."
## (cmd/jig/status.go's RenderStatus). The round 1 note (r1-f4) is never a
## slice, so it does not appear here - only in pr/review-notes.md, below.
Type `jig status JIG-1 --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 6s

Type "clear" Enter
Wait@5s

## Publish ships the branch, writes pr/fixture-repo.md and
## pr/review-notes.md into the store, and - on this local tracker - opens no
## pull request: "publish:\n  ticket: JIG-1\n  tier: none\npushed[1]{repo,
## head,squash}:\n  fixture-repo,<sha>,squashed\npr_body[1]{repo,path}:\n
## fixture-repo,JIG-1/pr/fixture-repo.md\npr_url[0]{repo,url,action}:\n..."
## (cmd/jig/publish.go's pushedTable, pr_body table and prURLTable - an
## empty pr_url table, since the local tracker has no PRCreator). With the
## github tracker this same call would also post pr/review-notes.md as the
## opened or updated pull request's first comment; the local tracker writes
## both files and posts nothing (README.md#what-a-published-pull-request-looks-like).
Type `jig publish JIG-1 --yes --store "$JIG_STORE_DIR"` Enter
Wait@120s
Sleep 8s

## The body: four `## ` sections. Intent is brief.md's own first section
## verbatim (this ticket has a brief, so its source is "brief", not
## "inferred" or "none"); What changed is one bullet per green slice with its
## short commit sha, the two gate-round fixes grouped after the rest under
## "Fixes from review:"; Demo is round 3's own recorded demo (the scenario
## scripts two small SVG frames for its clean round) - the summary, then
## each verified file in the one reference form gh --attach would rewrite
## for its kind - both are svg here, so each as a markdown image reference,
## `![caption](./<name>)` - left as a local path on this tracker since the
## local tracker uploads nothing; Verification names the oracle, the reviewed
## head, the revalidation tier, and points at the first comment (here, the
## second file below) for the findings themselves - never a changelog of
## slice ids or a "See pr/evidence.md" line a GitHub reviewer could not open
## (internal/verifydeliver/render.go's writePRBody and renderDemoSection).
Type `cat "$JIG_STORE_DIR/$JIG_TICKET/pr/fixture-repo.md"` Enter
Wait@10s
Sleep 14s

Type "clear" Enter
Wait@5s

## The detail the body only points at: round 3's own summary, then every
## finding from every round ordered by risk - r1-f1 (high) fixed by the
## round 2 recurrence's slice and cleared at round 3, r1-f3 (medium) fixed
## by its own slice and cleared at round 2, r1-f2 (low) dismissed by a
## human, r1-f4 (low) noted - each with its own risk rationale, and
## Coverage: the files the change touched (a git diff of the ship range)
## next to the paths a round actually read (internal/verifydeliver/
## render.go's writeReviewNotes). On a GitHub pull request this file is what
## publish posts as the first comment, right after it opens or updates it;
## here it is the file itself, since the local tracker posts nothing.
Type `cat "$JIG_STORE_DIR/$JIG_TICKET/pr/review-notes.md"` Enter
Wait@10s
Sleep 12s
