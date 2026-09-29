# demo/gate-demo.tape
#
# Records a clean gate round with its demo: `jig gate` dispatches a reviewer
# that reports nothing to fix, and then - because the round is clean and its
# reviewed head has no demo yet - a demo session that shows the change working.
# The gate report prints the demo line and the files recorded, the
# `demo.yaml` manifest the round left in the store is shown as it sits on
# disk, and the media themselves are listed where they live, under the jig
# home, never in the store. This is the flow ADR 0014
# (docs/adr/0014-demo-session-at-the-gate.md) exists to support.
#
# Every typed line here is checked against the real source, the same as
# gate-reviewer.tape's own header states: the report's rows against
# cmd/jig/gate.go (the round report's kv fields, rendered via axi.KV, and
# demoRows and the demo_media table beside them), the manifest against
# internal/verifydeliver/demo.go (recordedDemoYAML), and the media's location
# against internal/home (EvidenceDir). cmd/jig/gate_demo_e2e_test.go
# (TestGateDemoThroughMain) drives the same fixture and flow against the real
# binary in a Go test, but it checks the round's outcome (the report's rows, the
# manifest's keys and hashes, the media's bytes), never this tape's own typed
# lines - see demo/README.md for that caveat.
#
# Nothing on camera waits on an interactive prompt: the `demo` scenario's round
# 1 (testdata/fixture/scenario-branches/demo/gate/round-1) has a review result
# with no findings, so interactiveTriage prints, and asks, nothing at all, and
# the fake backend plays back that round's demo result and copies its two
# media files into the directory the dispatch names. So every Wait below is a
# bare Wait (VHS's own default prompt and pattern), never a Wait+Line anchored
# on a prompt's own trailing text.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME, the
# store, and the scenario all live under a fixture this tape builds for itself
# (demo/fixture, testdata/fixture/scenario-branches/demo), and every command
# below names them only by env var ($JIG_HOME, $JIG_STORE_DIR,
# $JIG_SCENARIO_DIR) - never an expanded path. The fixture's ticket is JIG-1:
# the hidden setup names it by $JIG_TICKET, and the commands on camera type its
# own id, as the other tapes do.

Output demo/out/gate-demo.gif
Output demo/out/gate-demo.mp4

Require jig
Require fixture

# Same grid as gate-reviewer.tape, for the same reason (see its own comment):
# `Set Columns` pins the count directly rather than leaving it to the runner's
# installed fonts, so the trimmed tail of the cursor's row is the same on every
# runner. No row this tape prints is wider than 160 columns, and the prompt
# never fills a row.
Set Shell "bash"
Set FontSize 14
Set Columns 160
Set Height 800
Set Padding 20
Set TypingSpeed 30ms
Set Framerate 30
Set CursorBlink false

Hide

# Build the fixture fresh, into its own scratch dir, and load its paths as env
# vars - after this line, no command ever types a literal path again. JIG_HOME
# is exported before the generator ever runs: VHS starts bash with the host's
# own environment appended, so a developer who already has a real JIG_HOME
# exported must never have it inherited here. `-scenario demo` layers the demo
# scenario over the base one, the same overlay TestGateDemoThroughMain builds.
Type `export DEMO_DIR="$(mktemp -d)"` Enter
Wait@5s
Type `export JIG_HOME="$DEMO_DIR/home"` Enter
Wait@5s
Type `eval "$(fixture -out "$DEMO_DIR" -scenario demo)"` Enter
Wait@30s

# A neutral working directory: nothing below depends on the shell's own cwd,
# but the prompt (and any relative-path slip) must never show the fixture's own
# tree.
Type `cd "$HOME"` Enter
Wait@5s

# Slices a, b, d build straight through; c pauses on q-001 (run exits 2), then
# the answer finishes it - the same shape buildFixtureTicket builds before the
# gate. The frontier is fully green afterwards, so the gate needs no --early.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Type `jig run "$JIG_TICKET" --answer q-001 "Casual." --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

# Wipe the hidden setup's own commands and output before the recording starts,
# so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## Round 1: the reviewer reports nothing, so the round is clean, and the round's
## own files are pushed before the demo session starts. The report's kv block
## then carries the demo: "gate:\n  ticket: JIG-1\n  round: 1\n  verdict:
## clean\n  model: claude-sonnet-5\n  intent: brief\n  scope: full\n  demo:
## recorded\n  demo_summary: ..." (axi.KV, gate.go's printGateReport and
## demoRows), and, because the demo recorded files, a demo_media table of each
## one's name, size and caption right after the empty findings and fix_slices
## tables. The demo never changed the verdict above it.
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 9s

## The manifest the round left in the store: status, the reviewed head, the
## session's summary, and per file the name jig gave it, its sha256 and size,
## and the caption (verifydeliver.recordedDemoYAML). No host path appears in it.
Type `cat "$JIG_STORE_DIR/JIG-1/gate/round-1/demo.yaml"` Enter
Wait@5s
Sleep 8s

## The media themselves: under the jig home, keyed by store id, ticket and
## reviewed head (home.EvidenceDir) - never in the store's git. One directory
## matches the glob, so ls prints only the file names jig chose, demo-<n>.<ext>.
Type `ls "$JIG_HOME"/evidence/*/JIG-1/*/` Enter
Wait@5s
Sleep 6s

## A second gate round on the same head has nothing new to review, and its demo
## already exists: the report says so and runs no second session.
Type `jig gate JIG-1 --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 8s
