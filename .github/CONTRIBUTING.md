# Contributing

jig is a Go project. Building needs only Go 1.27+; testing also needs `git`
on PATH, since the suite spawns real git commands against local, file-path
repos.

## Build and test

```sh
go build ./...
go test ./...
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
- Only `internal/gitx` spawns `git`; `lint.TestNoGitSpawnOutsideGitx` parses
  every other package and fails the build if one calls `os/exec` on a
  program that resolves to `git`.
- Every test gets its own `t.TempDir()`, and `JIG_HOME` is always overridden
  with `t.Setenv` so a test run never touches a real machine's jig home.

## Reporting a bug

Open an issue with the command you ran, what you expected, and what happened
instead. Include the output of `jig version`.

## Reporting a security issue

Do not open a public issue for a security vulnerability - see
[SECURITY.md](SECURITY.md).
