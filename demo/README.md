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
  slices those decisions queue, a second (delta) round, a clean third round,
  and `jig status`. It mirrors
  `cmd/jig/gate_reviewer_e2e_test.go`'s `TestGateReviewerRoundsThroughMain`
  keystroke for keystroke, but that test checks the round's outcome (the
  report's kv lines, the findings table, the store) - never the literal
  prompt wording the tape types against and waits on, so a prompt's wording
  can drift without that test failing. The tape's own comments cite the
  exact source line (`cmd/jig/triage.go`, `cmd/jig/gate.go`) each typed line
  and each Wait+Line anchor depends on; a bare `Wait` depends on VHS's own
  prompt and pattern instead, not a jig source line. A drift in a cited line
  shows up only as a failed or mismatched render, never as a failing test.

## Rule: fixture data only

A tape's terminal must never show a host path, a user name, or a real repo -
only fixture data (`internal/fixture`, `testdata/fixture/`), env var names
(`$JIG_HOME`, `$JIG_STORE_DIR`, `$JIG_SCENARIO_DIR`, `$JIG_TICKET`), or
paths relative to a ticket. Build the fixture with `demo/fixture` (see
Rendering locally, below), `eval` its `export` lines, and never type an
expanded path back out.

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
   anchor split across rows, and no prompt that exactly fills a row (its
   trailing space would wrap onto a blank cursor row).
4. Keep every typed command and every anchored `Wait+Line` checked against
   the real source (the command's own `_test.go`, and the command's own
   report-building code), not guessed - the way `gate-reviewer.tape`'s own
   comments cite `cmd/jig/triage.go` and `cmd/jig/gate.go` line by line. A
   bare `Wait` depends on VHS's own prompt and pattern, not a jig source
   line.
5. Push the branch; `.github/workflows/demo.yml` picks up any `demo/*.tape`
   automatically, no workflow change needed.
