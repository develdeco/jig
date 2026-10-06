# demo/gate-intent.tape
#
# Records a brief-less ticket through `jig gate` twice, with a `jig run`
# between the two rounds, a `cat` of the intent file the second round
# records, and a closing `jig status`: round 1, with no brief.md and no
# --intent, resolves to "none" - nothing states what the round is for; the
# run in between works round 1's own fix back to green; round 2, with
# `jig gate --intent`, records intent.md and resolves to "explicit"; the
# file itself shows the provenance it was recorded with; status then
# confirms the next-step hint points at publish, not run. This is the
# working-branch shape ADR 0012 (docs/adr/0012-intent-provenance.md) exists
# to support: a ticket adopted with no brief at all, gated round by round
# against a plain statement of intent instead.
#
# Every typed line here is checked against the real source, the same as
# gate-reviewer.tape's own header states: the two reports below are
# checked against cmd/jig/gate.go (the round report's kv fields, rendered
# via axi.KV, gate.go:156-170). cmd/jig/gate_intent_e2e_test.go
# (TestGateIntentNoneToExplicitThroughMain) drives the same fixture and
# flow against the real binary in a Go test, but it checks the round's
# outcome (the report's kv lines, intent.md itself), never this tape's own
# typed lines - see demo/README.md for that caveat.
#
# Nothing on camera waits on an interactive prompt: the scripted source
# (testdata/fixture/scenario/gate/round-1) raises a fix without ever
# dispatching a reviewer, round 1's own fix-1 has a canned attempt in the
# same base scenario so the `jig run` between the two rounds finishes
# unattended too, and round 2 has no gate/round-2 directory to play back at
# all - no triage prompt appears anywhere on camera, so every Wait below is
# a bare Wait (VHS's own default prompt and pattern), never a Wait+Line
# anchored on a prompt's own trailing text.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME,
# the store, and the scenario all live under a fixture this tape builds
# for itself (demo/fixture, testdata/fixture/scenario - the base scenario,
# no scenario-branches overlay), and every command below names them only
# by env var ($JIG_HOME, $JIG_STORE_DIR, $JIG_SCENARIO_DIR) - never an
# expanded path. The fixture's ticket is JIG-1: the hidden setup names it
# by $JIG_TICKET, and the commands on camera type its own id, as
# gate-reviewer.tape does.

Output demo/out/gate-intent.gif
Output demo/out/gate-intent.mp4

Require jig
Require fixture

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
# TestGateIntentNoneToExplicitThroughMain builds.
Type `export DEMO_DIR="$(mktemp -d)"` Enter
Wait@5s
Type `export JIG_HOME="$DEMO_DIR/home"` Enter
Wait@5s
Type `eval "$(fixture -out "$DEMO_DIR" -scenario "")"` Enter
Wait@30s

# The fixture always writes a brief.md; remove it so this ticket starts
# brief-less, the scenario this tape demonstrates - the same first step
# TestGateIntentNoneToExplicitThroughMain takes.
Type `rm "$JIG_STORE_DIR/$JIG_TICKET/brief.md"` Enter
Wait@5s

# A neutral working directory: nothing below depends on the shell's own
# cwd, but the prompt (and any relative-path slip) must never show the
# fixture's own tree.
Type `cd "$HOME"` Enter
Wait@5s

# Slice c pauses on q-001 (run exits 2); answer it so the frontier is
# fully green - neither gate round below needs --early.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Type `jig run "$JIG_TICKET" --answer q-001 "Casual." --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

# Wipe the hidden setup's own commands and output before the recording
# starts, so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## Round 1 (the frontier is already green, so no --early, and no --intent):
## the scripted round-1 (a single fix, alpha.Add missing a doc comment)
## reports without ever dispatching a reviewer, so there is no triage
## prompt to wait on - the report prints as soon as the command finishes.
## Brief-less and no intent.md yet, so resolveIntent's precedence falls all
## the way through to "none": "gate:\n  ticket: JIG-1\n  round: 1\n
## verdict: fix-slices\n  model: claude-opus-5\n  intent: none\n..."
## (axi.KV, gate.go:156-170).
Type `jig gate JIG-1 --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 5s

## Round 1 just queued fix-1 (its own fix for the missing doc comment);
## work it back to green before round 2, the same as a real terminal
## session would - gating again over unfinished work would only repeat
## this same fix-slices verdict.
Type `jig run JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 3s

## Round 2 (--intent; the frontier is fully green again, so still no
## --early): `jig gate --intent` writes intent.md (source explicit) before
## this round resolves, so the precedence now stops there instead of
## falling through to "none". The scenario has no gate/round-2 directory,
## so the source plays back nothing, and the fold - empty, since round 1
## never went through a reviewer - decides the round clean: "gate:\n
## ticket: JIG-1\n  round: 2\n  verdict: clean\n  model:
## claude-opus-5\n  intent: explicit\n..." (axi.KV, gate.go:156-170).
Type `jig gate JIG-1 --intent "make the greeting warmer" --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 5s

## The file round 2 recorded, provenance and all: the front matter names
## the source the report just printed, the body is the flag's own text
## ("---\nsource: explicit\n---\nmake the greeting warmer\n",
## store.renderIntent). The store dir is typed by env var, never expanded.
Type `cat "$JIG_STORE_DIR/JIG-1/intent.md"` Enter
Wait@5s
Sleep 4s

## Every slice is green and round 2 was clean, so the next-step hint now
## points at publish, not run (cmd/jig/status.go's own nextStepHint) -
## the same final check TestGateIntentNoneToExplicitThroughMain makes.
Type `jig status JIG-1 --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 6s
