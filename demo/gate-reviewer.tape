# demo/gate-reviewer.tape
#
# Records the gate reviewer's rounds: a fix batch with one dismissal, an ask
# kept with a decision, `jig run` building the fix slices those decisions
# queue, a second (delta) round, a clean third round, and `jig status`.
#
# Every typed line and every anchored Wait+Line below is checked, line by
# line (see the comments below), against the real source: cmd/jig/triage.go
# (interactiveTriage's exact prompt text) and cmd/jig/gate.go (the round
# report's kv fields, rendered via axi.KV). A bare Wait instead depends on
# VHS's own default prompt and pattern, not a jig source line.
# cmd/jig/gate_reviewer_e2e_test.go
# (TestGateReviewerRoundsThroughMain) scripts the same scenario and stdin
# against the real binary in a Go test, but it checks the round's outcome
# (the report, the findings table, the store), never the literal prompt
# wording this tape types against and waits on: a wording change there
# shows up only as a failed or mismatched render here, not as a failing
# test. See demo/README.md for that caveat.
#
# Each command's completion is detected by waiting for the shell prompt to
# return (VHS's own default bash prompt, a colored "> ", matched by its
# default WaitPattern ">$"), never by scanning the screen for output text: a
# Screen-scope Wait matches against the first `rows` lines of xterm's own
# scroll buffer, not against whatever is currently visible - before the
# screen has scrolled those lines still hold an earlier command's own
# output, and once it has scrolled they are old scrollback, so either way a
# screen-text wait can return before the right command has even run. A
# prompt still mid-command (the triage prompts below) is instead waited for
# with a Line-scope Wait anchored on that exact prompt's own trailing text,
# which only appears on the cursor's current line once the program itself
# prints it.
#
# Nothing on screen is a host path, a user name, or a real repo: JIG_HOME,
# the store, and the scenario all live under a fixture this tape builds for
# itself (demo/fixture, testdata/fixture/scenario-branches/reviewer), and
# every command below names them only by env var ($JIG_HOME, $JIG_STORE_DIR,
# $JIG_SCENARIO_DIR, $JIG_TICKET) - never an expanded path.

Output demo/out/gate-reviewer.gif
Output demo/out/gate-reviewer.mp4

Require jig
Require fixture

# The rule a Wait+Line anchor depends on: the anchor must be the trimmed
# tail of the cursor's physical row. So no anchored text may straddle a
# wrapped row, and no prompt may exactly fill a row: its trailing space
# then wraps onto a new, blank cursor row and the wait times out. A plain
# Width/FontSize pair leaves the column count to the runner's installed
# fonts (VHS has no native pty; it fits xterm.js in a headless browser
# against whichever font its FontFamily fallback list resolves to), and
# several counts break an anchor below: columns 125-130 break "again:"
# (triage.go:134's prompt) and columns 83-87 and 166-174 break
# "dismiss]:" (triage.go:185's prompt). `Set Columns` pins the grid
# directly instead: it is exclusive with `Set Width` (VHS command.go's
# ExecuteSetWidth/ExecuteSetColumns), and it leaves `Set Height` alone to
# size the render (Rows is the one that would conflict with Height). 160
# is 6 columns below the nearest failing band and wide of the widest
# report row this tape waits past (154 columns, gate.go's findings table
# for r1-f4). Changing it needs re-checking every Wait+Line anchor below
# against the new count.
Set Shell "bash"
Set FontSize 14
Set Columns 160
Set Height 800
Set Padding 20
Set TypingSpeed 50ms
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
# the same shape cmd/jig/gate_reviewer_e2e_test.go builds before round 1.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Type `jig run "$JIG_TICKET" --answer q-001 "Casual." --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s

# Wipe the hidden setup's own commands and output before the recording
# starts, so the first on-camera frame is a clean prompt.
Type "clear" Enter
Wait@5s

Show

## Round 1 (full): the real triage prompt, at a terminal, exactly as
## cmd/jig/triage.go's interactiveTriage renders it - two fixes (r1-f1
## high risk, r1-f2 low risk, sorted high first), one ask (r1-f3), one note
## (r1-f4, listed only, never prompted).
Type `jig gate "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
# triage.go:134 - "fixes: press Enter to accept all 2 and queue their fix
# slices, or list ids to dismiss - a dismissed finding is never raised
# again: " (trimmed tail: "again:")
Wait+Line@15s /again:$/
# Dismiss the low-risk trim fix (r1-f2); keep the high-risk overflow-doc
# fix (r1-f1) in the batch - the same split the e2e test scripts.
Type "r1-f2" Enter
# triage.go:185 - "keep or dismiss? keeping queues a fix slice and asks
# for your decision text; dismissing means this finding is never raised
# again. [k/keep/Enter=keep, n/no/d/dismiss=dismiss]: " (trimmed tail:
# "dismiss]:")
Wait+Line@15s /dismiss\]:$/
Type "k" Enter
# triage.go:213 - "decision for r1-f3 (optional, Enter to skip): "
# (trimmed tail: "skip):")
Wait+Line@15s /skip\):$/
Type "Use a warm, casual tone; no exclamation marks." Enter
# The round's own report: "gate:\n  ticket: JIG-1\n  round: 1\n  verdict:
# fix-slices\n  model: claude-sonnet-5\n  scope: full\n..." (axi.KV,
# gate.go:105-115), then the findings and fix_slices tables, then the
# command exits and the shell prompt returns.
Wait@15s
Sleep 1s

## Build round 1's fix slices (fix-1-alpha-test, fix-1-r1-f3) to green.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 1s

## Round 2 (delta): the doc-comment fix (r1-f1) recurs as this round's only
## fix - Enter alone accepts the batch of one, the same as the e2e test's
## "\n". The tone ask already cleared and the trim fix's dismissal carried
## forward (routeRound never re-offers a dismissed prior to triage), so
## neither prompts again.
Type `jig gate "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
# triage.go:134 with len(in.Fixes)=1 - "...press Enter to accept all 1..."
# (same trimmed tail as round 1's batch prompt: "again:")
Wait+Line@15s /again:$/
Enter
Wait@15s
Sleep 1s

## Build round 2's fix slice (fix-2-alpha-test) to green.
Type `jig run "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@60s
Sleep 1s

## Round 3: clean. The scenario's own round-3 review-result.json reports no
## findings, so routeRound's fixes/asks/notes are all empty and
## interactiveTriage prints, and asks, nothing at all - straight to the
## report and back to the shell prompt.
Type `jig gate "$JIG_TICKET" --backend fake --scenario "$JIG_SCENARIO_DIR" --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 1s

## The ticket's final state: every slice green and nothing outstanding to
## decide. `jig status` prints slices, questions/parked/stalled and any
## outstanding asks (status.go) - never notes, so the round 1 note stays
## in the store without appearing here.
Type `jig status "$JIG_TICKET" --store "$JIG_STORE_DIR"` Enter
Wait@15s
Sleep 2s
