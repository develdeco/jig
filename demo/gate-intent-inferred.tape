# demo/gate-intent-inferred.tape
#
# Records a brief-less ticket through a single `jig gate` round that infers
# its own intent, with no --intent and no brief.md: a synthetic local
# Claude Code transcript, planted under a demo HOME before the round runs,
# mentions the files the round's own scope diff touches, so
# internal/intent's matcher picks it; a summarizer session, played back
# through the fake backend, writes a short summary; and jig records it as
# intent.md with source "inferred" before dispatching the reviewer, so the
# round's own review.json carries that intent, not "none". It is
# gate-intent.tape's sibling: that tape shows the sources "none" and
# "explicit", this one shows "inferred", the one neither of its rounds
# reaches (ADR 0012, docs/adr/0012-intent-provenance.md, "Amendment:
# inference is built"; ARCHITECTURE.md, "Intent inference").
#
# Every typed line here is checked against the real source, the same as
# gate-reviewer.tape's own header states: the report below is checked
# against cmd/jig/gate.go (the round report's kv fields, rendered via
# axi.KV, gate.go:154-165, including the "intent: inferred" row, which
# carries no note because a successful inference leaves none - only the
# resolved source). e2e/gate_intent_infer_test.go
# (TestGateInfersIntentBriefLess) drives the same fixture, transcript and
# flow against the real binary in a Go test, but it checks the round's
# outcome (the report's kv lines, intent.md, review.json), never this
# tape's own typed lines - see demo/README.md for that caveat.
#
# Nothing on camera waits on an interactive prompt: the scenario's round-1
# review-result.json (testdata/fixture/scenario-branches/inferred-intent/
# gate/round-1/) reports no findings, so there is nothing to triage and the
# report prints as soon as the round finishes. The clean round is followed by
# its demo, which the scenario scripts too (the same directory's
# demo-result.json, with no media): an unscripted round would print a refused
# demo on camera, so the report also carries a recorded demo and its summary
# (ADR 0014, docs/adr/0014-demo-session-at-the-gate.md). Every Wait below is
# a bare Wait (VHS's own default prompt and pattern), never a Wait+Line
# anchored on a prompt's own trailing text.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME,
# the store, the scenario and the fixture repo's own working clone all live
# under a fixture this tape builds for itself (demo/fixture,
# testdata/fixture/scenario-branches/inferred-intent); a second, separate
# scratch directory stands in for the demo's own HOME, so the synthetic
# transcript is never planted under, or read from, the real operator's
# home; and every command below names all of it only by env var ($JIG_HOME,
# $JIG_STORE_DIR, $JIG_SCENARIO_DIR, $JIG_REPO_DIR, $JIG_TICKET,
# $DEMO_HOME), never an expanded path. The transcript's own "cwd" is
# $JIG_REPO_DIR: the fixture repo's working clone is also this demo's
# mapped clone (fixture.Build's project.InitProject call), the identity
# internal/intent checks a session's cwd against.

Output demo/out/gate-intent-inferred.gif
Output demo/out/gate-intent-inferred.mp4

Require jig
Require fixture

# Same grid as gate-reviewer.tape and gate-intent.tape, for the same
# reason (see either one's own comment): `Set Columns` pins the count
# directly rather than leaving it to the runner's installed fonts, so the
# trimmed tail of the cursor's row is the same on every runner. No row
# this tape prints is wider than 160 columns, and the prompt never fills a
# row.
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
# -scenario "inferred-intent" applies the scenario-branches overlay this
# tape demonstrates: the one TestGateInfersIntentBriefLess builds.
Type `export DEMO_DIR="$(mktemp -d)"` Enter
Wait@5s
Type `export JIG_HOME="$DEMO_DIR/home"` Enter
Wait@5s
Type `eval "$(fixture -out "$DEMO_DIR" -scenario "inferred-intent")"` Enter
Wait@30s

# A second, separate scratch directory stands in for the demo's own HOME -
# never JIG_HOME, and never the real operator's home - since local Claude
# Code transcripts live under $HOME/.claude/projects, not under jig's own
# JIG_HOME tree. HOME and USERPROFILE are both set, so it works the same
# whichever one os.UserHomeDir reads on the host.
Type `export DEMO_HOME="$DEMO_DIR/demo-home"` Enter
Wait@5s
Type `mkdir -p "$DEMO_HOME/.claude/projects/proj1"` Enter
Wait@5s
Type `export HOME="$DEMO_HOME"` Enter
Wait@5s
Type `export USERPROFILE="$DEMO_HOME"` Enter
Wait@5s

# The synthetic transcript: its cwd is $JIG_REPO_DIR (the fixture repo's
# working clone, also this demo's mapped clone), and its one assistant turn
# mentions every file the round's own scope diff touches - the five files
# the scenario's round-1 review-result.json lists under reviewed_paths, and
# the same list TestGateInfersIntentBriefLess's transcript uses. The
# timestamps inside the transcript are never read for the discovery window,
# only the file's own mtime is (internal/intent/reader_claude.go), so the
# `touch -d` below is what places this session inside it: the fixture's
# commits are all pinned to exactly 2026-01-01T00:00:00Z (fixture's
# identityEnv, session's fakeGitEnv), so the window is
# [2025-12-29T00:00:00Z, 2026-01-01T01:00:00Z] whatever real date this tape
# renders on. The trailing "Z" on the touch timestamp is load-bearing: GNU
# touch -d reads a bare "2026-01-01T00:30:00" in the host's own local
# timezone, so a host that is not UTC would place the file outside the
# window and the session would never match.
Type `cat > "$DEMO_HOME/.claude/projects/proj1/session-inferred.jsonl" <<TRANSCRIPT` Enter
Type `{"type":"user","cwd":"$JIG_REPO_DIR","timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"user","content":"Please clamp percentages and make the greeting casual."}}` Enter
Type `{"type":"assistant","cwd":"$JIG_REPO_DIR","timestamp":"2026-01-01T00:20:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"While working on this I edited alpha/alpha.go, alpha/percent.go, alpha/percent_test.go, beta/beta.go, beta/version.go."}]}}` Enter
Type `TRANSCRIPT` Enter
Wait@5s
Type `touch -d "2026-01-01T00:30:00Z" "$DEMO_HOME/.claude/projects/proj1/session-inferred.jsonl"` Enter
Wait@5s

# The fixture always writes a brief.md; remove it so this ticket starts
# brief-less, the same first step gate-intent.tape and
# TestGateInfersIntentBriefLess take.
Type `rm "$JIG_STORE_DIR/$JIG_TICKET/brief.md"` Enter
Wait@5s

# A neutral working directory: nothing below depends on the shell's own
# cwd, but the prompt (and any relative-path slip) must never show the
# fixture's own tree.
Type `cd "$DEMO_HOME"` Enter
Wait@5s

# Slice c pauses on q-001 (run exits 2); answer it so the frontier is
# fully green before the gate round below, the same a/b/c/d scenario
# gate-intent.tape and gate-reviewer.tape both drive first: the
# inferred-intent overlay replaces only gate/round-1/, never slices/.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Type `jig run "$JIG_TICKET" --answer q-001 "Casual." --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

# Wipe the hidden setup's own commands and output before the recording
# starts, so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## The frontier is already green, so no --early, and no --intent: nothing
## states this round's intent yet. --backend fake is load-bearing, unlike
## gate-intent.tape's own two rounds: `jig gate` with --scenario and no
## --backend runs the scripted GateSource (cmd/jig/gate.go's gateSourceFor,
## kept for compatibility), which reads a canned round outcome directly and
## never dispatches a session at all, and intent inference is
## reviewer-source code (internal/verifydeliver/review.go), so it would
## never run. --backend fake selects the reviewer source, played back
## through the fake session backend the way `jig run` already was above.
## Before dispatching the reviewer, internal/verifydeliver/intent_infer.go
## finds the synthetic session (its file mentions clear the match
## threshold), writes its excerpt under $JIG_HOME (never the store),
## dispatches a summarizer session through that same fake backend, and
## records intent.md with source "inferred" - so this round's reviewer
## dispatch, and the report below, both see "inferred", not "none". The
## scenario's round-1 reviewer finds nothing to report, so the round is
## clean with no triage prompt to wait on: "gate:\n  ticket: JIG-1\n
## round: 1\n  verdict: clean\n  model: claude-sonnet-5\n  intent:
## inferred\n  scope: full\n  demo: recorded\n  demo_summary: ...\n..."
## (axi.KV, gate.go:154-165, and demoRows beside them): the demo line is the
## round's clean-round demo, scripted with no media, so there is no
## demo_media table.
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@30s
Sleep 8s

## The file the round recorded, provenance and all: the front matter names
## the source the report just printed plus the agent, session and match
## score inferred from, and the body is the summarizer's own summary (the
## fake backend plays back the scenario's intent-result.json)
## (store.renderIntent). The store dir is typed by env var, never expanded.
Type `cat "$JIG_STORE_DIR/JIG-1/intent.md"` Enter
Wait@5s
Sleep 5s

## Every slice is green and the round was clean, so the next-step hint
## points at publish, not run (cmd/jig/status.go's nextStepHint).
Type `jig status JIG-1 --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 6s
