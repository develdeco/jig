# jig

jig drives a ticket from brief to an evidence-backed, opened pull request -
one dispatch loop over small, provable slices of work.

[![CI](https://github.com/develdeco/jig/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/develdeco/jig/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/develdeco/jig)](https://github.com/develdeco/jig/releases/latest)
[![License: MIT](https://img.shields.io/github/license/develdeco/jig)](LICENSE)

- Turns a brief into slices of work, each with a named oracle command that
  proves it done.
- Dispatches the frontier of ready slices to a build session, and resumes
  where a session left off.
- Re-verifies the branch's oracles in its own gate round, then a reviewer
  session finds, routes, and (with you, at a triage prompt) decides what
  becomes forward work before anything ships.
- Reconciles, revalidates, and opens the pull request itself, evidence
  attached.
- Keeps every ticket's state in a plain git repo, so progress survives any
  one session ending.

## Pipeline

```
brief     write brief.md + slices.yaml (you, with the intake skill)
  │
  ▼
run       dispatch the frontier of ready slices to a build session
  │
  ▼
gate      re-verify the ticket's branch's oracles
  │
  ▼
publish   reconcile, revalidate, and open the PR
```

Two moments need a human: deciding what the brief actually asks for, and
confirming before the PR goes out. See [ARCHITECTURE.md](ARCHITECTURE.md) for
what each stage reads and writes.

## Install

One line, no Go toolchain required. This downloads the release archive for
your platform, verifies it against `checksums.txt`, and installs `jig` to a
user directory (no sudo or admin rights):

```sh
curl -fsSL https://raw.githubusercontent.com/develdeco/jig/main/scripts/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/develdeco/jig/main/scripts/install.ps1 | iex
```

Set `JIG_VERSION` to install a specific release tag instead of the latest,
and `JIG_INSTALL_DIR` to change where `jig` is installed.

With Go 1.27 or newer:

```sh
go install github.com/develdeco/jig/cmd/jig@latest
```

Either way, install the session skills next:

```sh
jig skills install
```

This writes the skills - drafting a brief or chart (`intake`), routing a
stated intent to the right skill or command (`router`), running many
tickets in parallel (`fleet-liaison`), refreshing the store's platform
notes after a ticket lands (`platform-sync`), and mining repeated failures
for a skill or rule fix (`retro`) - to `~/.claude/skills`; pass `--project`
to install them under `./.claude/skills` of the current directory instead.

**Prerequisites:** `git` on PATH. The default session backend is `herdr`,
which needs `herdr` and the Claude Code CLI (`claude`), inside WSL on Windows
(`JIG_WSL_DISTRO` picks the distro). Pass `--backend headless` to drive a
local `claude -p` subprocess instead, which only needs `claude` on PATH.
`jig run` and `jig solve` check that the backend's program is on PATH before
they start and say what to install if it is not.
`graphify` is optional; jig falls back cleanly without it.
[`gh`](https://cli.github.com/) is needed for the GitHub tracker and
opening pull requests.

### Building from a clone

```sh
git clone https://github.com/develdeco/jig.git
cd jig
go build ./cmd/jig
```

## Quickstart

```sh
jig init --standalone                     # creates a sibling tickets store next to this repo
jig ticket new --title "Fix the thing"    # mints a ticket (T-1) in the store
# the intake skill drafts T-1/brief.md and T-1/slices.yaml with you
jig validate T-1                          # checks the brief, slices, and manifest agree
jig solve T-1 --backend headless --yes    # runs run, gate, and publish as one chain
```

`jig solve` dispatches slices, gates the branch, and publishes in one
chain. `--yes` skips the publish confirm and every gate round's triage
prompt (keeping every finding jig can route on its own): when a session asks a
question, `jig solve` stops, and you resume it with
`jig solve T-1 --yes --answer <qid> "<text>"`. With the standalone store above (`tracker: local`), publish
pushes `jig/T-1` and writes the PR body into the store instead of opening a
PR - set `tracker: github` in `project.yaml` and have `gh` on PATH to get
an opened PR. Run `jig run T-1` and `jig gate T-1` on their own as slices
need another attempt or a brief gets amended (`jig requeue
--from-brief-diff`). `jig status T-1` prints the ticket's slice and
question state at any point, and `jig <command> -h` prints that command's
flags.

## Session backends

A build session (`jig run`) runs against one of three backends, picked with
`--backend` (default `herdr`, or `fake` when `--scenario` is set): `fake`
replays a scripted scenario with no network calls, `headless` drives a local
`claude -p` subprocess, and `herdr` drives a remote agent through herdr -
natively off Windows, and on Windows inside a WSL login shell
(`JIG_WSL_DISTRO` picks the distro). All three read the same `slice.json`
and write the same `result.json`; see
[ARCHITECTURE.md](ARCHITECTURE.md#session-backends).

`jig gate` re-runs every manifest oracle on a fresh lease, then dispatches a
reviewer session on the backend `--backend` names (default `herdr`;
unlike `jig run`, `--scenario` alone does not switch this default to
`fake` - see below). `--scenario` alone, with no
`--backend`, keeps the old scripted gate source instead, for compatibility
with the pre-reviewer path: no reviewer session runs and there is no triage
prompt. `jig solve`'s own gate/fix-slice loop follows a narrower rule:
`--scenario` always selects that same scripted source, whatever `--backend`
says, so its reviewer only runs without `--scenario`. At a terminal, a
dispatched reviewer round stops for a triage prompt over what it found:
`--yes` skips it, keeping every fix and every ask whose build target
already resolves in full (a workspace and an oracle), and leaving an ask
missing a workspace, an oracle, or both for a human to decide later.

## Safety

The `headless` backend is wrapped by two screens before a tool call runs: a
structural command screen that parses each shell command instead of
pattern-matching it, so quoting or a `-C <path>` trick can't hide a `git
push` from it, and a secret-path screen that denies any tool-call path
shaped like a live credential (`.env*`, `*_key*`, `id_rsa*`, `~/.aws/**`, and
the like). `herdr` sessions are not screened yet. Every command pushes the
ticket store's own bookkeeping commits to the store's remote as it works;
only the ticket branch push is guarded, and only `jig publish` makes it,
after its interactive confirm (or `--yes`) has run - it refuses to push a
branch to a remote that isn't a local file path without one. Product
commits - the ones that land on your PR branch - use your own git identity,
not jig's. See [ARCHITECTURE.md](ARCHITECTURE.md#safety) for the full
model.

## Docs

- [CONTEXT.md](CONTEXT.md) - the vocabulary jig's code and docs share.
- [ARCHITECTURE.md](ARCHITECTURE.md) - the pipeline, store schema, module
  responsibilities, and the safety model in full.
- [docs/adr/](docs/adr/) - why each structural decision was made.
- [DECISIONS.md](DECISIONS.md) - the build log: what was ambiguous, what was
  chosen, and why.
- [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) - how to build, test,
  and change jig.
- [.github/SECURITY.md](.github/SECURITY.md) - how to report a
  vulnerability.
- [skills/](skills/) - the session skills that ship embedded in the binary.

## Roadmap

Coming in v0.2: `ask` findings parked as questions instead of left kept by
`--yes` or a non-terminal run, a review guide rendered into PR evidence
from recorded rounds, review-eval scoring from recorded triage decisions,
Jira and Linear tracker adapters, `gate`'s `--pr` mode for reviewing a PR
someone else opened, the `fleet` and `retro` binary verbs for working many
tickets and mining repeated failures, and design-facet oracles. Later:
more than one repo per project, and nix packaging.
