# Demo tapes

A jig PR's demo is a [VHS](https://github.com/charmbracelet/vhs) tape: a
plain-text script of keystrokes and waits that CI renders, from the branch
head, into a GIF and an MP4. No local install is needed to review one, and a
tape can never show a stale or hand-edited recording - CI builds the fixture
and drives the real `jig` binary every time.

## What each tape shows

- **`gate-reviewer.tape`** - the gate reviewer's three rounds against the
  `reviewer` fixture scenario: round 1's real triage prompt (a fix batch with
  one dismissal, an ask kept with a decision), `jig run` building the fix
  slices those decisions queue, a second (delta) round, a clean third round
  with its demo, and `jig status`. It mirrors
  `cmd/jig/gate_reviewer_e2e_test.go`'s `TestGateReviewerRoundsThroughMain`
  keystroke for keystroke, but that test checks the round's outcome (the
  report's kv lines, the findings table, the store) - never the literal
  prompt wording the tape types against and waits on, so a prompt's wording
  can drift without that test failing. The tape's own comments cite the
  exact source line (`cmd/jig/triage.go`, `cmd/jig/gate.go`) each typed line
  and each Wait+Line anchor depends on; a bare `Wait` depends on VHS's own
  prompt and pattern instead, not a jig source line. A drift in a cited line
  shows up as a failed or mismatched render, not as a failing end-to-end
  test. (`cmd/jig/triage_test.go` separately pins what the fix batch, ask
  and decision prompts say their answers do, so rewording that fails a
  unit test as well.)
- **`gate-intent.tape`** - a brief-less ticket through the base fixture
  scenario (no overlay): round 1 with no brief and no `--intent` resolves to
  `intent: none`, a `jig run` works round 1's fix back to green, round 2 with
  `jig gate --intent` records `intent.md` and resolves to `intent: explicit`,
  a `cat` shows that file with its provenance, and `jig status` points at
  publish. It mirrors
  `cmd/jig/gate_intent_e2e_test.go`'s `TestGateIntentNoneToExplicitThroughMain`
  keystroke for keystroke, with the same outcome-vs-wording split as
  `gate-reviewer.tape` above. Neither round here dispatches a reviewer, so
  there is no triage prompt and every `Wait` is bare.
- **`gate-intent-inferred.tape`** - `gate-intent.tape`'s sibling for the third
  intent source: a brief-less ticket through the `inferred-intent` fixture
  scenario, with a synthetic local Claude Code transcript planted under a
  separate demo `HOME` (and `USERPROFILE`) before the round runs, its own file
  mentions matching the round's scope diff. A single `jig gate --backend fake`
  round infers its intent from that transcript - the fake backend plays back
  the summarizer's result - and reports `intent: inferred`, then the round's
  demo (the scenario scripts one with no media, so the report says `demo:
  recorded` and gives its summary); a `cat` shows `intent.md` with its
  provenance (agent, session, match score), and `jig status` points at
  publish. It mirrors `e2e/gate_intent_infer_test.go`'s
  `TestGateInfersIntentBriefLess` keystroke for keystroke, with the same
  outcome-vs-wording split as `gate-reviewer.tape` above. The scenario's round
  reports no findings, so there is no triage prompt and every `Wait` is bare.

- **`adopt-branch.tape`** - a branch built outside jig, fixed in place: a
  branch of three commits the tape's hidden setup builds from the fixture's own
  scenario patches and pushes, `jig ticket new` minting a ticket for it and
  offering to adopt a branch, `jig gate --branch` adopting it and finding a
  fix, `jig run` building that fix on it, a `git log` of the build lease
  showing the fix commit sitting on top of the author's three (whose tip is
  still origin's), the next `jig gate` reviewing the branch again and coming
  back clean, `jig status`, which names the adopted branch and suggests `jig
  publish`, `jig publish --yes` shipping the branch as it is (the output says
  "not squashed (branch already on origin)"), and a `git log` of the fixture
  remote's branch: the author's three commits from the top of the recording,
  unchanged, with jig's fix commit and publish's memorize commit on top. It
  mirrors
  `e2e/adopt_branch_test.go`'s `TestGateBranchRoundFixesBuildOnTheReviewedBranch`
  command for command, with the same outcome-vs-wording split as the tapes
  above, but that test hand-writes its branch and checks where the fix commit
  landed, that the author's commits kept their shas and what the ticket
  recorded, not the tape's own typed lines. Nothing here dispatches a reviewer,
  so there is no triage prompt and every `Wait` is bare (the publish runs with
  `--yes`), and no intent is inferred: the reports say `intent: none`, where a
  reviewer round on an adopted branch infers one like any brief-less ticket's.
  The fixture's tracker is local, so no pull request is opened.
- **`gate-demo.tape`** - a clean gate round with its demo, against the `demo`
  fixture scenario: the reviewer reports nothing, so the round is clean, and
  the demo session that follows shows the change working (the fake backend
  plays back two small SVG frames drawn from the fixture's own test cases).
  The gate report prints the demo line and the files recorded, `cat` shows the
  `gate/round-1/demo.yaml` manifest the round left in the store, `ls` shows
  the media where they live (under the jig home, never in the store), and a
  second round on the same head says its demo already exists. It mirrors
  `cmd/jig/gate_demo_e2e_test.go`'s `TestGateDemoThroughMain`, with the same
  outcome-vs-wording split as the tapes above. No interactive prompt appears
  on camera, so every `Wait` is bare.

## Rule: fixture data only

A tape's terminal must never show a host path, a user name, or a real repo -
only fixture data (`internal/fixture`, `testdata/fixture/`), env var names
(`$JIG_HOME`, `$JIG_STORE_DIR`, `$JIG_SCENARIO_DIR`, `$JIG_REPO_DIR`,
`$JIG_TICKET`), or paths relative to a ticket. Build the fixture with
`demo/fixture` (see Rendering locally, below), `eval` its `export` lines, and
never type an expanded path back out.

## How CI renders and uploads them

`.github/workflows/demo.yml` runs on every push to a non-`main` branch that
touches `cmd/**`, `internal/**`, `demo/**`, `skills/**`, `testdata/**`,
`go.mod`, `go.sum` or the workflow itself, plus `workflow_dispatch`. It is
**not a required check**: a rendering failure never blocks a push or a PR,
only the demo it would have produced.

The job builds `jig` and `demo/fixture` onto `$PATH`, installs `vhs`, `ttyd`
and `ffmpeg` at the versions pinned in the workflow's `env:` block, renders
every `demo/*.tape`, and uploads `demo/out/**` as an artifact named
`demo-<short sha>` (14-day retention).

Fetch a run's artifact with the GitHub CLI:

```sh
gh run download --name demo-<short sha>
```

(`<short sha>` is the branch head's short commit hash - the same one
`git rev-parse --short HEAD` prints for that commit.) `gh run list
--workflow demo.yml` finds the run when you only have the branch name.

## Rendering locally (Linux, WSL, or macOS)

The tape's own script needs bash and a POSIX userland (`mktemp`), so render
it there rather than on native Windows.

Install the same versions `.github/workflows/demo.yml` pins (its `env:`
block is the source of truth if this drifts):

- Go (see `go.mod` for the version)
- [`vhs`](https://github.com/charmbracelet/vhs) v0.12.1:
  `go install github.com/charmbracelet/vhs@v0.12.1`
- `ttyd` and `ffmpeg`, from your distro's package manager (Debian/Ubuntu:
  `apt-get install ttyd ffmpeg` - exact versions are pinned in CI, not
  locally; a materially different `ttyd`/`ffmpeg` can still change a
  render's exact timing or pixels)

Then, from the repo root:

```sh
go build -o /tmp/jig-demo-bin/jig ./cmd/jig
go build -o /tmp/jig-demo-bin/fixture ./demo/fixture
export PATH="/tmp/jig-demo-bin:$PATH"
mkdir -p demo/out
vhs demo/gate-reviewer.tape
```

The rendered `demo/out/gate-reviewer.gif` and `.mp4` are gitignored -
they are a CI artifact, never committed.

## Adding a tape

1. Write the flow as a Go test first if the behavior does not already have
   one driving the real binary (cmd/jig's own end-to-end tests are the
   template) - a tape with no such test can silently drift from what the
   binary actually prints.
2. Build the fixture scenario the tape needs under
   `testdata/fixture/scenario-branches/` if the base scenario does not
   already cover it (see `internal/fixture`'s package doc).
3. Write `demo/<name>.tape`: `Output demo/out/<name>.gif` and
   `Output demo/out/<name>.mp4`, `Require jig` (and `Require fixture` if it
   builds its own fixture), hide the setup (`Hide`/`Show`), and prefer
   `Wait`/`Wait+Line` over a fixed `Sleep` wherever the pinned VHS version
   supports it - a tape that only sleeps either idles needlessly or races a
   slow CI runner. Avoid `Wait+Screen`: it matches against the first `rows`
   lines of xterm's own scroll buffer, not against whatever is currently
   visible - before the screen has scrolled those lines still hold an
   earlier command's own output, and once it has scrolled they are old
   scrollback, so either way a match can be stale or unreachable. Wait for
   a command's own completion with a bare `Wait` (its default Line scope
   matches VHS's own shell prompt returning) and for a still-running
   program's own prompt with `Wait+Line` anchored on that prompt's trailing
   text. Pin the grid with `Set Columns` rather than `Set Width`, so the
   column count does not depend on the runner's fonts, and choose it so
   every `Wait+Line` anchor is the trimmed tail of the cursor's row: no
   anchor split across rows, and no prompt whose text up to its trailing
   space exactly fills a row (the space would wrap onto a blank cursor row).
4. Keep every typed command and every anchored `Wait+Line` checked against
   the real source (the command's own `_test.go`, and the command's own
   report-building code), not guessed - the way `gate-reviewer.tape`'s own
   comments cite `cmd/jig/triage.go` and `cmd/jig/gate.go` line by line. A
   bare `Wait` depends on VHS's own prompt and pattern, not a jig source
   line.
5. Push the branch; `.github/workflows/demo.yml` picks up any `demo/*.tape`
   automatically, no workflow change needed.
