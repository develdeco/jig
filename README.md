# jig

jig takes a ticket from brief to an evidence-backed, opened pull request,
attached to the coding agent session you already run. It starts no session and
picks no model: it watches your commits, proves each slice with its own oracle
command, and records the review and the publish.

[![CI](https://github.com/develdeco/jig/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/develdeco/jig/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/develdeco/jig)](https://github.com/develdeco/jig/releases/latest)
[![License: MIT](https://img.shields.io/github/license/develdeco/jig)](LICENSE)

- Turns a brief into slices of work, each with a named oracle command that
  proves it done.
- Attaches to your harness session (Claude Code, Codex, Gemini CLI, Cursor,
  GitHub Copilot, OpenCode) through hooks, skills, MCP tools and the CLI, so
  you build with your own tools, subagents and model.
- Proves green itself: when a commit names a slice (a `Jig-Slice:` trailer),
  jig runs the slice's oracle in the background on a detached worktree of that
  commit and journals the result. A session never waits on a test run, and green
  is never what a model claimed.
- Tells the session what it needs, once, when it matters: the slice's context,
  an answer to a question it asked, a failed run, a stall, a brief that changed
  under verified work. An unattended session is held back at Stop until its
  loose ends are tied.
- Records the review as a gate round that a fresh-context agent, one jig did not
  start, reads and closes, and checks that the reviewer did not build the change.
- Renders a lean pull request body - why, what changed, the change working, how it
  was verified - with the full review detail as its first comment, and publishes
  the reviewed head exactly as you pushed it.
- Keeps every ticket's state in a plain git repo, so progress survives any one
  session ending.

## Pipeline

```
brief     write brief.md + slices.yaml (you, with the intake skill); jig validate approves it
  │
  ▼
claim     jig here, next, claim, context: where the work is, what is ready, take a slice
  │
  ▼
build     your session commits with a Jig-Slice trailer; jig verify proves each commit in the
  │       background and the session hears the outcome
  ▼
gate      re-verify the branch's oracles; a fresh reviewer records the round
  │
  ▼
publish   render the PR body, push the reviewed head, open or record the PR
```

Three moments need a person: deciding what the brief actually asks for,
answering a question a session asked, and confirming before the pull request goes
out. See [ARCHITECTURE.md](ARCHITECTURE.md) for what each stage reads and writes.

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

Either way, set up your agents next:

```sh
jig setup --global --artifacts --yes
```

That installs the project skills (`here`, `intake`, `build`, `review`, `publish`) in
`~/.claude/skills` and `~/.agents/skills`, adds jig's hook entries to Claude Code's
`~/.claude/settings.json`, points git's global `core.hooksPath` at jig's wrappers (each
repository's own hooks still run first), and adds a few lines to `~/.claude/CLAUDE.md`
that send an agent to `jig here`. `--yes` says it may edit those files; without it you
are asked at a terminal, and a script gets `NEEDS_INPUT`. `--helpers` adds the skills
`jig-init` and `jig-attach`, which let an agent start a project or attach a repository,
and extra skills install by name (`retro` mines repeated failures for a skill or rule
fix; `platform-sync` refreshes the store's platform notes after a ticket lands).
`jig doctor` lists what is in place and what each missing piece costs, and
`jig setup --global --uninstall` takes away exactly what was written. See
[Setups](#setups) for the other cases: a project only, a team repo, other harnesses,
a cloud session.

**Prerequisites:** `git` on PATH with an identity (`git config --global user.name` and
`user.email`: jig authors every store commit as you), and a harness to work in - Claude
Code is the one proven end to end; the hook files for Codex, Gemini CLI, Cursor, GitHub
Copilot and OpenCode are written from their documentation.
[`graphify`](https://github.com/Graphify-Labs/graphify) (`uv tool install graphifyy`) is
optional: a project that opts in with `context: {graphify: true}` in its store config
gets, from `jig context`, the code a local code graph links to a slice's goal, and jig
falls back cleanly without it.
[`gh`](https://cli.github.com/) is needed for a GitHub remote's pull-request
host and opening pull requests.

### Installing an unreleased build

`main` can carry fixes that no release has yet. It has no release
archives, so it installs with Go 1.27.2 or newer:

```sh
go install github.com/develdeco/jig/cmd/jig@main
jig version
jig setup --global --artifacts --yes
```

`go install` puts `jig` in `$(go env GOPATH)/bin`, or `GOBIN` when set,
and that directory must come first on your PATH: otherwise a `jig` the
installers put in their own directory still answers. `jig version` shows
which one does. A build of `main` between releases reports a Go
pseudo-version such as `v0.1.2-0.20260928203114-640df78698ec`, not a
release tag. Run `jig setup --global --artifacts --yes` again once it does
(`jig doctor` says when a stamp is from another version), so the skills match the
binary. From a clone, `go install ./cmd/jig` does the same for the
checked-out tree. `main` has passed CI, but not the checks a release runs
on its own installed binaries.

## Quickstart

Once per machine, set up your agents (see [Install](#install)); then, in a repo:

```sh
cd my-repo
jig init                                     # defines the project: a jig.yaml here and a sibling my-repo-tickets store
jig ticket new --title "Fix the thing"       # mints a ticket (JIG-1) in the store
# the intake skill drafts JIG-1's brief.md and slices.yaml with you
jig validate JIG-1                           # checks the brief, slices and manifest agree, and approves the brief
```

From here you work in your harness session, in the repo. Telling your agent
"build JIG-1" is enough, since its `build` skill runs these commands; they are
what it runs:

```sh
jig here                                     # where the work is: project, store, branch, ticket, slices
jig next JIG-1                               # what is ready, and the branch to create
git switch -c JIG-1-fix-the-thing
jig claim JIG-1 --slice a                    # take a slice (refused when unattended and not ready)
jig context JIG-1 --slice a                  # its brief sections, earlier slices, the oracle and its time
# ...build...
git commit -m "Fix the thing" --trailer "Jig-Slice: a"
```

The commit starts `jig verify` in the background (git's global `post-commit`
hook, or the harness's PostToolUse hook, whichever sees it first): the slice's
oracle runs on a detached worktree of that commit, the result is journaled, and
the session hears it at its next tool call or prompt. When every slice is green:

```sh
jig gate JIG-1                               # opens a round and prints its request path
```

A fresh-context agent that jig did not start - a subagent, or another session
with the `review` skill - then reviews it:

```sh
jig review context JIG-1                     # what to review, against what, and how to report
jig finding JIG-1 --file src/a.go --line 12 --title ... --detail ... --action fix --risk medium --risk-rationale ...
jig review done JIG-1 --reviewed src/a.go --summary "..."
```

The round is recorded when `review done` returns. Then:

```sh
jig publish JIG-1 --body                     # renders the PR body and review notes, changes nothing else
jig publish JIG-1                            # pushes the reviewed head and opens or updates the PR (asks first)
jig publish JIG-1 --pr <url>                 # or: you opened the PR yourself; record it
```

jig infers the pull-request host from the shipped repo's own `remote:`: the
sibling store and a repo whose remote is a local path have no host, so publish
only pushes the branch and writes the PR body into the store; a repo whose
remote is on github.com, with `gh` on PATH, gets its pull request opened or
updated. `jig status JIG-1` prints the ticket's slice and question state at any
point, `jig tui` browses and drives the whole store (answer, decide asks, gate,
publish), `jig events --follow` streams everything as it happens, and
`jig <command> -h` prints that command's flags.

A session that is stuck asks instead of guessing: `jig ask` parks its slice, and
`jig answer JIG-1 q-001 "..."` (or the TUI's `a`) sends it back to work, with the
answer's provenance (person or agent) recorded. A brief that is amended after
its slice verified is re-queued with `jig requeue JIG-1 --from-brief-diff`; one
stalled or env-blocked slice by id with `jig requeue JIG-1 --slice <id>`.

A ticket's id is `<key>-<n>`: a single-repo project's store derives its one key
(`JIG`) from the repo's own name, so `jig ticket new` needs no `--key`. A store
whose config's `keys:` declares more than one - say a multi-repo project using
each repo's acronym as a key - passes `--key <KEY>` to pick which one a new
ticket mints under; leaving it out then is refused, naming the declared keys
and their meanings. A key removed from the config only stops new mints under
it: its tickets keep working, and an id never changes.

Work spanning tickets starts from a chart instead: the intake skill drafts
`charts/<name>/map.md` and `charts/<name>/tickets.yaml`, then

```sh
jig graduate <name>    # creates the chart's tickets, writing their ids back into tickets.yaml
```

creates each entry that has no id yet, in file order, and records any
blockers it declared into that ticket's own `ticket.yaml`. Each created
ticket then gets its own `brief.md` and `slices.yaml` and follows the same
flow above; re-running `jig graduate <name>` after the chart's fog clears
creates just the new entries. An entry's `key:` picks which declared key it
mints under, same as `jig ticket new --key` (the intake skill writes it); an
undeclared key on any entry refuses the whole run before any of the chart's
tickets are created.

## Setups

`jig setup` puts jig's pieces where your agents read them, and the scope is
always spelled out: `--global` (you, on this machine), `--here` (the project you
are in; `jig attach` is the same, and refuses outside a project) or
`--project <name>`. The pieces are `--skills`, `--hooks`, `--githook` and
`--hint`; `--artifacts` is all four. `jig doctor` lists what is in place, what
each missing piece costs and the command that adds it, and `--uninstall` takes
away exactly what the same flags wrote. The files that are yours (a settings
file, a memory file) are only edited after a yes: `--yes`, or a prompt at a
terminal. Which scenario you are in decides which command to run:

| You are | Run | What you get |
|---|---|---|
| working on your own machine in Claude Code | `jig setup --global --artifacts --yes`, then `jig init` in each repo | skills, hooks, git's global `post-commit` hook and a hint in your own configuration; the repo gets only a `jig.yaml` |
| working in a repo and want nothing visible in it | `jig attach` (or `jig setup --here`) | the untracked form: one skill stub, `.claude/settings.local.json` and `CLAUDE.local.md`, each listed in `.git/info/exclude` |
| on a team repo that should carry jig | `jig attach --tracked` | the committed form: full skills under `.claude/skills` and `.agents/skills`, hooks in `.claude/settings.json`, a line in `AGENTS.md`; inert without the binary: the repo declares, never installs |
| in several repos under one directory | `jig init` in that directory (an orchestrator over the repos nested in it), `jig repos sync` as they come and go, `jig attach --members` | one project, one store, each nested repo set up too |
| in another harness | `jig setup --global --harness codex,gemini` (`all` for every one) | hook files for Codex, Gemini CLI, Cursor, GitHub Copilot and OpenCode, written from their documentation; only Claude Code is proven end to end |
| in a cloud session | `jig setup --cloud` prints the environment script | it installs the binary and runs `jig setup --global --artifacts --helpers --yes`; nothing is written here |
| in a harness with no hooks, or a crew | `jig mcp` (tools over stdio), or `jig observe --event ...` | the same verbs as tools, and events reported by flags |
| unattended | start the session yourself (a crontab, a scheduler) and have it `jig claim --unattended` | an unmet claim condition refuses, and a Stop is refused once per loose end |
| ready to share the project | `jig share` | sets or creates the remotes, records the store in `jig.yaml`, and pushes the store and the repos |

`jig init` runs only at a project's root, reads the case off the folder (a
directory holding repos is an orchestrator, one that holds none is a single-repo
project) and says which it picked before it writes. A project is local until
`jig share` publishes it.

## A branch with no brief

A branch you built yourself needs no brief and no slices. Commit it (or push
it), mint a ticket, and hand the branch to the gate:

```sh
jig ticket new --title "Add retry"    # mints T-2 in the store
jig gate T-2 --branch add-retry       # reviews the branch and adopts it
# a fresh reviewer: jig review context / finding / done
jig publish T-2                       # ships add-retry as it is, and opens or updates its PR
```

The first `--branch` records `add-retry` as the ticket's branch, and its tip as
the ticket's start sha; a `jig claim` on a branch that names no ticket records
that branch the same way. A ticket adopts one branch for good, so naming another
`--branch` later is refused, and so is adopting a branch for a ticket whose own
branch jig already verified commits on.

The ticket needs no brief. A round resolves its intent the way any ticket's
does (see below): the brief, else `--intent` or `--doc`, else one inferred from
your Claude Code sessions - an excerpt of the one that best matches the change,
which the reviewer reads - else none. For a branch you built yourself, the
session it matches is your own, by the files the branch changes.

The round reviews the copy of the branch in your clone (its committed state; a
dirty working tree is never read), or origin's when that is ahead. When your
clone's branch and origin's each have commits the other lacks, the round stops
with `BRANCH_DIVERGED`: jig merges nothing that is not its own, so join the two
and rerun.

`jig publish` ships the head the last clean round reviewed, exactly as it is:
nothing is reconciled, squashed or committed on your branch, and origin's branch
is fast-forwarded. If the branch already has an open pull request into the
target - yours, say - its body is replaced with the one publish wrote (its title
stays) and no second one is opened; otherwise, with a GitHub host, one is
opened. A closed or merged pull request, or one into another base, is not that
one and is left as it is. Publish never forces: it stops with
`PUBLISH_NOT_FAST_FORWARD` when a push lands while it runs, before it writes
anything, and it ships only the head the last clean round reviewed
(`PUBLISH_UNREVIEWED_HEAD`): if the branch moved after the round, run `jig gate`
again. The same holds for a binding intent, `brief.md` or `intent.md`: edit it
after the last clean round and publish refuses with
`PUBLISH_UNREVIEWED_INTENT`, before writing anything, with the same fix. Since
nothing is rewritten, the ticket goes on working: another round reviews the
branch as it stands, and another publish is a fast-forward that updates the same
pull request.

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
  and a pick stands: an agent picks, among the recordings made at the
  shipped head or an ancestor of it, the ones that show the change, composes
  them into flows and writes them as a picks file, which `jig publish --picks`
  takes (with recordings to pick and no file, `jig publish --body` prints
  `picks: pending` and writes the request, `<head>.picks.json` under the jig
  home's `evidence/` directory, for the agent to answer). The section is the pick's summary and each flow under a
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
  it reviewed, and one line counting the review's
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

## What jig tells your session

With the hooks installed (`jig setup --hooks`, or all of `--artifacts`), a directory
in a jig project gets jig's voice at four moments, and a directory in no project
gets nothing at all:

- **Session start**: the text of `jig here` - the project, the ticket the branch
  binds, its slices and their states, open questions, verifications still
  running - lands in the model's context, so it knows where it is before the
  first prompt.
- **A commit lands**: git's `post-commit` hook and the harness's PostToolUse
  both start `jig verify` for it, and whichever sees it first does; it runs
  once. The slice is the commit's `Jig-Slice:` trailer, else the session's
  claim.
- **The next tool call or prompt**: what the session has not heard yet, once -
  a verification's outcome (a failed run comes with the tail of its output), an
  answer to a question it asked, a stall (the same failure twice: change the
  approach), a brief that changed under verified work, a commit outside every
  slice's workspace, a ticket near or over its `jig budget`.
- **Stop**: a person at the keys is shown what is left (`jig: ...`). A session a
  claim marked `--unattended` is refused once for each loose end - a
  verification still running, tracked files not committed, a failed run or a
  correction, a ticket you approved with `jig publish --approve` that is not yet
  published - and a `stop_hook_active` stop with nothing new always ends. An open
  question is shown and holds nothing.

`jig observe` always exits 0 and prints nothing when it has nothing to say: a
hook that complains does harm. If a hook seems silent, `jig doctor` says whether
the harness has ever called it, and `<jig home>/logs/observe.log` has what went
wrong.

Spend is a record, never a wall: at Stop and SessionEnd jig sums the session's
tokens and list-price dollars from the transcript into the ticket's journal,
`jig budget JIG-1 --tokens 5M --usd 20` sets a limit, and `jig status` shows
both. A model with no price is named as unpriced (put it in `<jig home>/prices.yaml`)
instead of being guessed.

## Working with several people

A store is one git repo several people push to. Until it is told otherwise it is
in single-main mode: everyone pushes main, as always. `jig store migrate
--writers` moves it to writers mode: each clone pushes only a branch of its own,
`writers/<name>` (the name is `git config jig.writer`, else the host name), and
reads the others' read-only, with a union merge for the NDJSON files. The facts
only one person may hold go through an append-only claims log on a branch of its
own: a minted id, and the ticket's driver - `jig claim` takes it, another
person's unreleased one is `DRIVER_HELD` (`jig claim --take` takes the ticket
over), and `jig status` names it. A rewritten writer branch is refused and
reported, never repaired. Every store commit is authored by the identity git
resolves in the store clone, so the history names the person, and a clone with
none is refused (`IDENTITY_REQUIRED`).

## GitHub mirror

jig can mirror the store onto GitHub issues and a GitHub Project, from
inside every command: after each checkpoint it syncs every ticket and
chart, best-effort - a GitHub failure warns and never fails the command,
and the next checkpoint retries. Whatever that sync found prints in the
output of the command it rode in on: every issue created, every drift line,
anything a publish-safety hit skipped. To turn it on, add a `github` entry
to the store config's `trackers:` list (`store.yaml`, or `project.yaml` at schema 2):

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
in separate stores shows on one board - keep each store's declared keys
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

## Upgrading a store

A store's config declares its `schema_version`. jig reads schema 2 and 3, and
`jig store migrate` moves a store up one step at a time (`--to <n>` must name the
next version, so each step is one reviewable store pull request). Every command
but `jig store migrate` (and `jig help`/`jig version`) refuses a store still at
`schema_version: 1`, naming `jig store migrate` as the next step.

### From v1 to v2

Version 2 puts real tickets under `tickets/<id>/`, gives every id an area key
(`keys:`, e.g. `STORE-1`), and replaces the older `tracker:`/`ticket_format`
pair with `trackers:`. Write a rename map naming every ticket's new area key:

```yaml
keys:
  STORE: the store's layout, ids, records and git sync
  GRAPH: tickets, charts and the order between them
tickets:
  T-1: GRAPH
  T-23: STORE
```

Every ticket the store has must appear in `tickets:`, under a key
`keys:` declares; a ticket left out, one the store does not have, or an
undeclared key is refused, naming every such entry. `jig store migrate
--map <file> --dry-run` prints the resulting rename (old id, new id,
title) and every file the migration would move, delete or rewrite,
without changing anything - run it first to check the plan.

`jig store migrate --map <file>` then carries it out: it refuses a store
that is not clean and level with its origin, or whose tickets have a
lease still held in this machine's pool; it warns, without refusing, about
any `jig/<old id>` branch still on a repo's origin (merge or close it first;
jig names a ticket's branch `<new id>-<slug>` from then on, and the old branch
still binds to the ticket through the alias); it moves each ticket folder to
`tickets/<new id>/`, keeps the old id as an alias (`jig status`, `jig validate`
and every other command resolve it from then on), rewrites `project.yaml` and
every chart's `blocked_by` links, and commits once, on the current branch,
without pushing. Push the branch yourself and open the usual pull request; the
first sync after it merges updates each mirrored issue in place.

### From v2 to v3

Version 3 keeps the store's config in `store.yaml`; what describes the project
(its name, kind and repos) moves to the `jig.yaml` marker in the repo. A v2 store
works unchanged until you migrate it. `jig store migrate` (no map) swaps
`project.yaml` for `store.yaml` in one commit by you, keeping every key the old
file had, and writes `jig.yaml` into the mapped clone of the project's one repo,
uncommitted: commit it. It refuses a project of several repos or none, and a
project with no clone mapped (`jig map <repo>=<path>` first). `jig store migrate
--writers` is a different move, to [writers mode](#working-with-several-people),
and takes neither `--map` nor `--to`.

## Safety

jig starts no coding session. What it runs on your machine is `git`, `gh` for the
pull-request host, the oracle commands of your own repo's manifest (in a detached
worktree of the commit, bounded by the manifest's `timeout:`, 30 minutes by
default), and, detached, itself. It edits your settings and memory files only
after a yes, keeps everything in them that is not its own, refuses a file it
cannot read rather than writing over it, and recognizes what it wrote by its
command, stamp or marker, so an uninstall takes out only that. Its hooks exit 0
and record nothing for a directory in no jig project. The machine ledger records,
for sessions in jig projects only, the first 400 bytes of each command a session
ran and the files it touched; it lives under the jig home on your machine and is
never in the store.

A gate round with no brief and no explicit intent also reads your local Claude
Code transcripts. With a mapped clone for the repo, it looks under
`~/.claude/projects` for a session that ran in that repo and touched the files of
the change, and writes an excerpt of that session's own user and assistant text
(tool calls and tool results are dropped) under the jig home. The reviewer reads
the excerpt; no model summarizes it, and nothing of its text is pushed - the
store carries only the request's metadata (the session id and the diff files),
with the excerpt's local path cleared. Nothing switches this off but stating the
intent yourself, with a `brief.md`, `--intent` or `--doc`, so do that when a
session's text must not reach a reviewer.

Every command pushes the ticket store's own bookkeeping commits to the store's
remote as it works (in writers mode, to your own branch and the claims branch
only, never main). The ticket branch is different: only `jig publish` pushes it,
after its confirmation (`--yes`, a person's recorded approval of the head, or a yes
at the prompt) - it refuses to push a branch to a remote that isn't a local file
path without one - and `jig share` pushes after you confirmed its plan. jig makes
no commit on your branch (bar `jig share` committing `jig.yaml` once, as you, after
you confirmed its plan). Commits in the store use your own git identity, as git
resolves it in the store clone, never one of jig's own.

The headless sessions of the reviewer evaluation (the one place the source tree
still starts a model, not part of the `jig` binary) are not a security boundary:
a granted session's shell and file reads run with the operator's own rights. They
are wrapped by two screens before a tool call runs, a structural command screen
and a secret-path screen, both accident guards and not confinement. See
[ADR 0008](docs/adr/0008-headless-permission-model.md) for why a denylist of path
spellings can't close that gap, [ARCHITECTURE.md](ARCHITECTURE.md#safety) for the
full model and [.github/SECURITY.md](.github/SECURITY.md) for what a report can
cover.

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

Not built yet, and named in [ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md):
`jig brief show` (a local viewer for a brief's illustration), `jig watch`, counting the
session's own run of the oracle, cloud sessions and Remote Control (`jig setup --cloud`
prints the environment script and nothing more), a live check of any harness but Claude
Code, and `jig store migrate` for a multi-repo v2 store. Further out: Jira and Linear
tracker adapters, design-facet oracles, and nix packaging.
