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
- Reconciles, revalidates, and opens or updates the pull request itself,
  with a lean body - why, what changed, how it was verified - and the full
  review detail posted as its first comment.
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
publish   reconcile, revalidate, and open or update the PR
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

With Go 1.27.2 or newer:

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
[`graphify`](https://github.com/Graphify-Labs/graphify) (`uv tool install graphifyy`) is optional: a project that opts in with `context: {graphify: true}` in `project.yaml` gets each builder the code a local code graph links to its goal, and jig falls back cleanly without it.
[`gh`](https://cli.github.com/) is needed for a GitHub remote's pull-request
host and opening pull requests.

### Installing an unreleased build

`main` can carry fixes that no release has yet. It has no release
archives, so it installs with Go 1.27.2 or newer:

```sh
go install github.com/develdeco/jig/cmd/jig@main
jig version
jig skills install
```

`go install` puts `jig` in `$(go env GOPATH)/bin`, or `GOBIN` when set,
and that directory must come first on your PATH: otherwise a `jig` the
installers put in their own directory still answers. `jig version` shows
which one does. A build of `main` between releases reports a Go
pseudo-version such as `v0.1.2-0.20260928203114-640df78698ec`, not a
release tag. Run `jig skills install` once it does, so the skills match the
binary. From a clone, `go install ./cmd/jig` does the same for the
checked-out tree. `main` has passed CI, but not the checks a release runs
on its own installed binaries.

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
`jig solve T-1 --yes --answer <qid> "<text>"`. jig infers the pull-request
host from the shipped repo's own `remote:`: the standalone store above points
at a local path, so publish only pushes `jig/T-1` and writes the PR body into
the store instead of opening a PR; a repo whose remote is on github.com, with
`gh` on PATH, gets its pull request opened automatically - no `project.yaml`
setting is needed either way. Run `jig run T-1` and `jig gate T-1` on their own as slices
need another attempt or a brief gets amended (`jig requeue T-1
--from-brief-diff`), or to clear one stalled or env-blocked slice by id
(`jig requeue T-1 --slice <id>`). `jig status T-1` prints the ticket's
slice and question state at any point, and `jig <command> -h` prints that
command's flags.

Work spanning tickets starts from a chart instead: the intake skill drafts
`charts/<name>/map.md` and `charts/<name>/tickets.yaml`, then

```sh
jig graduate <name>    # creates the chart's tickets, writing their ids back into tickets.yaml
```

creates each entry that has no id yet, in file order, and records any
blockers it declared into that ticket's own `ticket.yaml`. Each created
ticket then gets its own `brief.md` and `slices.yaml` and follows the same
`validate`/`solve` flow above; re-running `jig graduate <name>` after the
chart's fog clears creates just the new entries.

## A branch built outside jig

A branch you built yourself needs no brief and no slices. Push it, mint a
ticket, and hand the branch to the gate:

```sh
jig ticket new --title "Add retry"    # mints T-2 in the store
jig gate T-2 --branch add-retry       # reviews the pushed branch and adopts it
jig run T-2                           # builds what the round queued, on add-retry
jig gate T-2                          # reviews add-retry again, fixes included
jig publish T-2                       # ships add-retry as it is, and opens or updates its PR
```

The first `--branch` records `add-retry` as the ticket's branch, and its tip
as the ticket's start sha. From then on `jig gate`, `jig run` and `jig solve`
work on that branch: the fix slices a round queues are built in jig's own
lease on top of your commits, and the next round reviews the branch with them.
`jig solve` runs the loop and publishes. Only `jig publish` pushes: until then
jig's commits wait in the build lease. A ticket adopts one branch for good, so
naming another `--branch` later is refused, and so is
adopting a branch for a ticket jig has already built on: a green result in its
journal says so, whichever version of jig wrote it, and adopting another
branch would strand the commits on the ticket's own.

The ticket needs no brief. A round resolves its intent the way any ticket's
does (see below): the brief, else `--intent` or `--doc`, else, for a reviewer
round, one inferred from your Claude Code sessions, else none. For a branch you
built yourself, the session it matches is your own, by the files the branch
changes. Pass `--backend headless` to `gate` and `run`, as in the quickstart,
when herdr is not installed.

Until jig has built on the branch it is yours, and each round and each build
takes it as it is now: push to it, or rewrite it, and they follow. Once jig has
built on it, a push of yours leaves each side with commits the other lacks: the
next build stops with `BRANCH_DIVERGED`, and so does the next round, which
would otherwise call a head clean that is not the branch's. jig merges nothing
that is not its own: integrate the two in the lease the error names, then
rerun. Once jig's commits have been pushed, rounds review the branch on origin.
A machine whose build lease lacks commits that jig built on another machine
neither reviews nor builds without them: it stops with `BUILD_LEASE_MISSING`
until they are pushed from the machine that built them.

`jig publish` ships the branch as it is. Only history that is not yet on origin
is squashed, and your branch is already there: your commits keep their shas,
jig's fix commits sit on top of them, and so do publish's own (the merge of the
target when it moved, and the memorize commit with the retrieval notes), and
origin's branch is fast-forwarded to the result. The output says "not squashed
(branch already on origin)". If the branch already has an open pull request
into the target - yours, say - its body is replaced with the one publish wrote
(its title stays) and no second one is opened; otherwise, with a GitHub host,
one is opened. A closed or merged pull request, or one into another
base, is not that one and is left as it is. Publish never forces: when your
branch and jig's have diverged it stops with `BRANCH_DIVERGED`, and with
`PUBLISH_NOT_FAST_FORWARD` when a push lands while it runs, before it writes
anything either way, and it ships only the head the last clean round reviewed
(`PUBLISH_UNREVIEWED_HEAD`): if the branch moved after the round, run `jig gate`
again. The same holds for a binding intent, `brief.md` or `intent.md`: edit it
after the last clean round and publish refuses with
`PUBLISH_UNREVIEWED_INTENT`, before writing anything, with the same fix.
Since jig's commits are on origin under their own shas, the ticket goes
on working: another round reviews origin's copy, and another publish is a
fast-forward that updates the same pull request. That holds for a ticket's own
`jig/<ticket>` after a publish that pushed it as it is, too. Its first publish
squashes it, and jig's unsquashed commits in the build lease then stand diverged
from the squash: the next round, build or publish stops with `BRANCH_DIVERGED`
until you merge origin's branch into the build lease, as the error says.

## What a published pull request looks like

A reviewer who opens a pull request `jig publish` opened or updated finds up
to four things in its body, and nothing else: why the change exists, what
changed, the change working, and how it was verified.

- **Intent** - the body of the first section of the ticket's `brief.md`, or
  the text of an explicit `--intent`/`--doc`. Left out entirely when the last
  clean gate round's intent was inferred from your own Claude Code sessions,
  or when there was none: an inferred intent is a hint for the reviewer, not
  a human's own words, and it never reaches a pull request. Left out too when
  there is no such text to show - a brief with no first section with text
  under it (no `## ` heading at all, or one with nothing below it), an
  intent file that is front matter and nothing else - rather than publishing
  a heading that states no intent, or the whole of a brief whose later
  sections are not the intent. A brief that bound as the intent but has no
  section with text to publish is the one omission publish warns about
  (`jig: the pull request body for ... has no ## Intent section`, on stderr
  before it asks you to push), so you can edit the brief and gate again
  instead of editing a pull request already open. A whole design doc passed
  to `--doc` is shown as it reads, with its own headings moved below the
  section level so the body still has these four and nothing else.
- **What changed** - on an adopted branch, your own pre-adoption commits
  first (subject and short sha, from the merge base with the target up to
  the start sha recorded at adoption); then one bullet per green slice, in
  slice order, with the first line of its goal and its short commit sha; fix
  slices from a gate round's findings are grouped after the others as fixes
  from review. One line per bullet: a fix slice's goal is the prompt jig
  wrote from the gate's findings, and those findings belong in the comment
  below, not in the body.
- **Demo** - present only when the build recorded its end-to-end scenarios
  and a pick stands: a short session picks, among the recordings made at the
  shipped head or an ancestor of it, the ones that show the change, and
  composes them into flows (`--backend` and `--scenario` pick its backend, as
  for `jig run`). The section is the pick's summary and each flow under a
  `###` heading, the files staged as `rec-<n>.<ext>`, an image as a markdown
  image reference (`![caption](./rec-<n>.<ext>)`) and a video as a plain
  bullet (`- ./rec-<n>.<ext>: caption`) - the one reference form each kind
  actually gets rewritten in. A recording publish cannot verify anymore
  (missing, or changed since the build recorded it) is left out of the choice
  and named on stderr; a pick that is refused or fails, like a build that
  recorded nothing, leaves the section out entirely, and stderr says why.
  With a GitHub host the
  media are attached to the pull request itself (`gh ... --attach`),
  which rewrites a recognized image reference to the uploaded URL in place
  but not a video's bare path; publish reads the pull request back, moves
  the upload URL `gh` appended for each such file to where its own
  reference stands, and edits the body again, reporting on stderr any
  reference still unrewritten after that. With no host (the standalone
  store above, whose repo remote is a local path) every reference is left
  as it is, since nothing is uploaded.
- **Verification** - the oracles green at the last clean round and the head
  it reviewed, the revalidation tier, and one line counting the review's
  findings by how they ended (fixed, dismissed, noted, asked), pointing at
  the pull request's first comment for the detail.

That first comment is `pr/review-notes.md`: the last round's own summary,
every finding across every round ordered by risk, each one with its
rationale and how it ended - fixed by a slice and cleared at a later round,
kept with your own decision, dismissed by a human, noted, or still open and
asked - and coverage: the files the change touched, against the files the
reviewer read (every round's own reviewed paths). With a GitHub host
this is posted once, right after the pull request is created or updated
(`gh pr comment`); a post that fails is a warning, not a failed publish - the
pull request stands and the file stays in the store for you to post by hand.
With no host (the standalone store above) both files are written under
`<ticket>/pr/` and nothing is posted.

`demo/publish-body.tape` plays this through end to end with no pull-request host:
a ticket's gate rounds fixing, dismissing, and noting findings - the last of
them clean - then `jig publish` and both files as it leaves them - see
[demo/README.md](demo/README.md).

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

Every round is judged against a resolved intent, not necessarily a brief:
a ticket's own `brief.md` wins when there is one, else `jig gate --intent
"<text>"` or `--doc <path>` records an explicit `intent.md` (refused when
the ticket already has a brief.md - amend that instead), else a
dispatched reviewer round tries to infer one from your local Claude Code
sessions for this repo (see Safety below), recorded in `intent.md` as a
hint the reviewer is told may be partial or wrong, else the round has no
intent to judge against at all, and the reviewer is told so plainly. Both
flags work in every mode, not only `--branch`, and each gate report
prints which one this round resolved to.

The gate records nothing. What a pull request shows of the change working
comes from the build: the end-to-end scenarios a builder's green oracle run
executes can leave recordings (screenshots, videos, terminal captures) in the
directory jig names in `JIG_RECORD_DIR`, and `jig publish` has a short
session pick among them - see
[What a published pull request looks like](#what-a-published-pull-request-looks-like).

## GitHub mirror

jig can mirror the store onto GitHub issues and a GitHub Project, from
inside every command: after each checkpoint it syncs every ticket and
chart, best-effort - a GitHub failure warns and never fails the command,
and the next checkpoint retries. Whatever that sync found prints in the
output of the command it rode in on: every issue created, every drift line,
anything a publish-safety hit skipped. To turn it on, add a `github` entry
to `project.yaml`'s `trackers:` list:

```yaml
trackers:
  - github:
      repo: your-org/your-tracking-repo
      project: https://github.com/users/your-org/projects/7
```

`repo:` is where issues live - named, not derived, since GitHub cannot
transfer an issue from a private repo to a public one once one appears.
The docs' own rule of thumb: the product repo for a one-repo project, a
dedicated tracking repo for several. `project:` is the GitHub Project's
URL, `users/<login>` for a personal project or `orgs/<org>` for an
organization's. Both are required; jig creates the project's `Status` and
`Store ID` fields itself the first time it syncs - there is no setup
command.

The token `gh auth token` prints needs the `project` scope (`gh auth
refresh -s project`) alongside whatever `gh` already needs for pull
requests; a project jig cannot resolve (gone, or that scope missing) looks
the same from here and is refused the same way.

When the issue repo is public, put a `publish-terms.txt` in the jig home
(one term per line, matched case-insensitively, `#` comments and blank
lines ignored) naming anything project-specific that must never reach a
public issue - a codename, an internal hostname - beyond the built-in
checks (email addresses, home directory paths, GitHub/Anthropic/AWS token
prefixes, private key headers). A hit skips that one ticket's or chart's
issue - GitHub keeps whatever it had - and `jig trackers sync` exits
non-zero when anything was skipped; a private issue repo is never scanned.

Several stores may point at the same issue repo and project, so work kept
in separate stores shows on one board - keep each store's `ticket_format`
distinct (or otherwise ensure the ids never collide), since jig adds no
store identity of its own to an issue.

Run `jig trackers sync [--dry-run] [--store <path>] [--project <name>]` to
sync on demand and see what changed: created, updated and reopened or
closed issues, links added and removed, board fields set, and every drift
line - an edit made directly on GitHub to a field jig owns (title, body,
open/closed state, parent and blocked-by links, a board item's Status or
Store ID) is reported and overwritten with the store's value again.
`--dry-run` reads GitHub and the store and reports what it would change,
writing to neither - the cutover's own check, since a dry run that never
looked at GitHub could not tell you whether the two already agree.

A sync from inside another command stops after 2 minutes, warning how many
tickets or charts still have no GitHub issue and pointing at `jig trackers
sync`; one mutation per second against a first sync's 5 or more per ticket
(create, update, up to two links, up to three board fields) means a
sizeable store's first sync needs several checkpoints to drain, each
making progress. `jig trackers sync` itself runs with no such bound, since
it is run on demand rather than from inside another command, so the
cutover and an L3 migration should each run one full `jig trackers sync`
instead of waiting out the backlog across several commands.

## Safety

The `headless` backend is not a security boundary: a granted session's
shell and file reads run with the operator's own rights, unconfined to the
lease, so run jig only against code - and on a machine - you would already
hand that same shell. It is wrapped by two screens before a tool call runs:
a structural command screen that parses each shell command instead of
pattern-matching it, so quoting or a `-C <path>` trick can't hide a plainly
spelled `git push` from it, and a secret-path screen that denies a tool
call naming a live credential (`.env*`, `*_key*`, `id_rsa*`, `~/.aws/**`,
`~/.ssh/**`, and the like), by its spelling and by where it resolves on
disk. Both screens are an accident guard, not confinement: a shell glob, a
variable, a junction, or a hard link can still reach a credential the
literal check would have caught, a git alias or a variable (`-c
alias.x=push`, `$(echo git) push`) can still reach a push the command
screen would have caught, and the resolution step deliberately never
follows a network share or a device path, since doing so can dial a remote
host. See [ADR 0008](docs/adr/0008-headless-permission-model.md) for why a
denylist of path spellings can't close that gap, and what would. A
`headless` session gets nothing else it doesn't need: jig's own settings
grant its shell and file reads only through a passing screen, and its file
edits only inside the lease and its own `result.json` - though the operator's own user settings, which still load on
top, can grant more. The lease's `.claude/settings.json` is not loaded,
since that file is part of
the code under review, while its `CLAUDE.md` is carried in from the lease's
committed tree, with a 64 KiB size cap, since that is the repo telling the
session how it works. Before each screened session starts, jig checks that
the screen still answers, and refuses to run one behind a screen that is
missing or broken - though that check only binds the start of a session,
not its whole life. A session is bounded in time, so a wedged one fails
instead of hanging. `herdr` sessions are not screened yet.

A reviewer round with no brief and no explicit intent also reads your
local Claude Code transcripts. With a mapped clone for the repo, it looks
under `~/.claude/projects` for a session that ran in that repo and touched
the files of the change, writes an excerpt of that session's own user and
assistant text (tool calls and tool results are dropped) under the jig
home, and has a model summarize it, in a scratch directory of its own
rather than in the code under review (and, with the headless backend, with
that summarizing session's own transcript not saved in Claude Code's data).
The summary is pushed as the store's `intent.md`, beside the request's
metadata (the session id, the diff files and the excerpt's local path) and
the accepted result; the excerpt itself stays under the jig home on your
machine. Nothing switches this off but stating the intent yourself, with a
`brief.md`, `--intent` or `--doc`, so do that when a session's text must not
reach a model.

Every command pushes the ticket store's own bookkeeping commits to the
store's remote as it works; only the ticket branch push is guarded, and only
`jig publish` makes it, after its interactive confirm (or `--yes`) has run -
it refuses to push a branch to a remote that isn't a local file path without
one. Product commits - the ones that land on your PR branch - use your own
git identity, not jig's. See [ARCHITECTURE.md](ARCHITECTURE.md#safety) for
the full model.

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
`--yes` or a non-terminal run, review-eval scoring from recorded triage
decisions, Jira and Linear tracker adapters, `gate`'s `--pr` mode for
reviewing a PR someone else opened, the `fleet` and `retro` binary verbs
for working many tickets and mining repeated failures, and design-facet
oracles. Later: more than one repo per project, and nix packaging.
