# Contributing

jig is a Go project. Building needs only Go 1.27+; testing also needs `git`
on PATH, since the suite spawns real git commands against local, file-path
repos.

## Build and test

```sh
go build ./...
go test -timeout 30m ./...
```

`gofmt -l .` should print nothing, and `go vet ./...` should be clean. See
Testing rules below for what the suite does and doesn't touch.

## Making a change

- Keep commits to one logical change each, with a conventional commit message
  (`fix:`, `feat:`, `docs:`, `ci:`, and so on).
- A change to the command screen, the secret-path screen, or the guarded push
  needs a test that exercises the behavior it changes - see the Safety
  section of [ARCHITECTURE.md](../ARCHITECTURE.md#safety).
- [ARCHITECTURE.md](../ARCHITECTURE.md) describes how the packages fit
  together. A lint test checks that every directory its module table lists
  actually exists, so a renamed or removed package can't leave a stale row
  behind - it does not check the reverse, so a new `internal/` package can
  still go unlisted. [CONTEXT.md](../CONTEXT.md) is the vocabulary the code
  and docs share.
- Write an ADR (`docs/adr/000N-title.md`, following the existing ones) when a
  decision constrains future design - a choice later work has to live with
  or deliberately reverse. Record a judgment call in `DECISIONS.md` instead
  when it resolved something ambiguous during a build but doesn't bind
  future design - the "what was ambiguous, what was chosen, and why" log at
  the top of that file. Correct a stale fact in place; record a new decision
  as a new entry.

## Pull requests

- Keep a pull request to one concern. If a reviewer would need a map to
  follow the body, split the change instead of writing the map.
- The body has three parts, and nothing else:
  - **What**: a few bullets. The first one says why the change exists.
  - **Demo**: when the change is visible, show it working. A screenshot
    when one frame shows it; a GIF or a video when it is a flow, such as a
    prompt and what follows it. Attach media with `gh pr create --attach` or
    `gh pr edit --attach` (gh 2.99 or later) rather than committing it.
    Leave the section out when nothing is visible, as with a docs-only or
    purely internal change. jig's own flows are recorded as VHS tapes that
    CI renders from each branch head: see [demo/README.md](../demo/README.md).
  - **Verification**: the gates you ran, and anything you checked beyond
    them.
- Everything else a reviewer might want - contract changes, where to look,
  what is safe to skim, decisions and deviations - goes in one "Reviewer
  notes" comment posted when the pull request opens.
- Answer each review round in a reply comment: each item, and the commit
  that addressed it. Edit the body only to correct a statement that became
  false, never to append review history.

## Session skills

`skills/*/SKILL.md` are lint-enforced (`lint/skills_test.go`): each file must
be at most 100 lines and 6000 bytes, and its frontmatter needs a `name:`
matching the directory name and a `description:` of at most 50 words. The
`router` skill's intent table is also lint-checked for its required rows,
with the pr-mode, fleet and retro rows each required to carry a `v0.2`
annotation.

## Testing rules

- Tests never touch the network. Every git remote used in a test is a bare,
  file-path repo.
- Every package whose tests run git wires `internal/gittest` into
  `TestMain` (`gittest.Run`), which points git at a generated, hermetic
  config and stops background maintenance from outliving the test process.
- Only `internal/gitx` runs git, as a program or in process:
  `lint.TestNoGitSpawnOutsideGitx` parses every other package and fails the
  build if one calls `os/exec` on a program that resolves to `git`, and
  `lint.TestNoGoGitOutsideGitx` if one imports go-git.
- No test in `internal/` edits process-global state: the environment
  (`t.Setenv`, `os.Setenv`, `os.Unsetenv`), the working directory
  (`t.Chdir`, `os.Chdir`), or a package-level variable declared in the
  package's non-test code. A dependency a test must replace comes through
  the package's `Deps` or an argument. `TestMain` may set the process up
  once, before any test runs. `lint.TestNoGlobalStateEditInInternalTests`
  enforces this as a ratchet; a debt list names the files that may still
  offend today, and only shrinks as offenders are fixed.
- Every test gets its own `t.TempDir()`, and its own jig home, so a test
  run never touches a real machine's: packages take the jig home root as an
  argument (`fixture.Opts.Home`, `verifydeliver.Deps.Home`,
  `frontier.Deps.Home`, `pool.Acquire`), and a test passes a `t.TempDir()`;
  a test that runs `cmd/jig` or the jig binary, which read `JIG_HOME`, sets
  it with `t.Setenv`, except `e2e`'s: they hand the subprocess `JIG_HOME`
  (and, for the tests built on `newFixture`, `HOME` and `USERPROFILE`) in its
  own environment (`jigEnv` and `runJig`), never through the test process's,
  so they run in parallel with each other.

## Tests against the real Claude Code CLI

`go test ./...` runs every session backend against a stub `claude`. The
live CLI tests run jig's headless backend through the real `claude` on
your PATH instead, against a scripted Messages API on loopback, so they
need no sign-in and spend no tokens:

```sh
JIG_LIVE_CLAUDE=1 go test -count=1 -run Live ./internal/session ./e2e
```

`internal/session`'s test checks the headless permission model against the
CLI, and `e2e`'s runs README's Quickstart, from `jig skills install` to a
published branch. Set `JIG_E2E_BINARY` to an installed jig to run the e2e
suite against it instead of a binary built from the tree. CI's
`claude-cli` job runs both on all three platforms, on every pull request,
every push to main and once a day, with the latest CLI release installed,
and every release runs the Quickstart with its own binaries, as they are
installed, before and after it is published.

## Live review eval

`internal/revieweval` scores the gate reviewer against a labeled corpus
(`testdata/revieweval`) by dispatching a real reviewer session against it.
It is opt-in, since it runs real model sessions, which need a signed-in
`claude` CLI and spend tokens:

```sh
JIG_REVIEWEVAL_BACKEND=headless go test -count=1 -timeout 0 -v -run TestEvalLive ./internal/revieweval
```

A case's result is a measurement, not a pass/fail gate on the build: each
round reads PASS, PROVISIONAL (nothing failed, but some findings still
need a person's label) or FAIL, and a refused round stays a measurement.
A round that comes back Failed fails the test itself: a dispatch failure,
a judge error, the judge changing the case repo, or a reviewer that wrote
no result at all, since the eval has nothing to score there. Cases run one at a time and
the report is rewritten after each one, so the command above (`-timeout
0` disables Go's own default) keeps whatever finished on disk if the run
is interrupted. Environment variables: `JIG_REVIEWEVAL_MODEL` (reviewer
model; default the rung `jig gate` itself picks once an unattended
ticket's builders have already used the cheapest rung),
`JIG_REVIEWEVAL_JUDGE_MODEL` (judge model; default the reviewer model),
`JIG_REVIEWEVAL_CORPUS` (default `testdata/revieweval`), and
`JIG_REVIEWEVAL_REPORT` (the text report's path; the JSON report is
written beside it with a `.json` suffix). Left unset, the report still
lands on disk, under the user cache dir (`os.UserCacheDir()`, then
`jig/revieweval/report.txt`, falling back to `os.TempDir()` only when there
is no cache dir) - the run logs that path once at the start, so it never
has to be found by guessing.

## Reporting a bug

Open an issue with the command you ran, what you expected, and what happened
instead. Include the output of `jig version`.

## Reporting a security issue

Do not open a public issue for a security vulnerability - see
[SECURITY.md](SECURITY.md).
