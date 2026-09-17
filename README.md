# jig

jig drives a ticket from raw ask to an opened, evidence-backed PR through
Brief → Build → Gate → Publish. One Go binary does the machinery - dispatching
sessions, tracking slice state, screening commands, opening the PR - while
session skills supply judgment: reading a brief, writing code, reviewing a
diff. All state lives in a remote-backed git repo (the *store*), so a ticket's
progress survives any single session ending. Two moments need a human:
deciding what a brief actually asks for, and confirming before the PR goes
out.

## Install

```sh
git clone git@github.com:develdeco/jig.git
cd jig
go build ./cmd/jig
```

Or install straight from the module, without cloning:

```sh
GOPRIVATE=github.com/develdeco go install github.com/develdeco/jig/cmd/jig@latest
```

`GOPRIVATE` is required because the repo is private - it tells `go install` to
fetch the module directly over git instead of through the public module proxy,
which cannot see a private repo.

## Skills

Session skills (drafting a brief, classifying a gate finding, and the rest of
the judgment calls in `skills/`) ship embedded in the `jig` binary. Install
them where a session expects to find them with `jig skills install`: with no
flags it writes to `~/.claude/skills` (every session on the machine); with
`--project` it writes to `./.claude/skills` (this repo only).

## Quickstart

```sh
go build ./cmd/jig                        # builds the jig binary
./jig init --standalone                   # creates a sibling tickets store next to this repo
./jig ticket new --title "Fix the thing"  # mints a ticket (T-1) in the store
# write T-1/brief.md and T-1/slices.yaml - the intake skill drafts both with you
./jig validate T-1                        # checks the brief, slices, and manifest agree
./jig run T-1                             # dispatches the frontier of queued slices to a build session
./jig gate T-1                            # runs a review + re-verification round over the ticket's branch
./jig publish T-1                         # reconciles, revalidates, and opens the PR
```

Run `jig ticket new`, `jig run`, and `jig gate` again as slices need more
attempts or a brief gets amended (`jig requeue --from-brief-diff`); `jig
solve` runs `run` → `gate` → `publish` as one chain and pauses for a question
or the publish confirm.

## CLI reference

`jig` with no arguments prints this:

```
usage: jig <command> [flags]
commands[12]{name,summary}:
  init,"initialize a store (standalone, or store + clones)"
  ticket,"mint a new ticket: jig ticket new --title <t>"
  solve,"run the full chain: run, gate, publish"
  run,dispatch the frontier of queued slices
  requeue,requeue slices touched by a brief edit
  gate,run a gate round over the ticket's branch
  publish,"reconcile, revalidate, and open the PR"
  status,print a ticket's slice and question state
  validate,"check a ticket's brief, slices, and manifest"
  version,"print jig's version, commit, and go runtime"
  skills,"jig skills install: ship the session skills with the binary"
  _screen,"hidden PreToolUse hook: reads a tool call on stdin"
flags{init}[3]{flag,usage}:
  --standalone,create a sibling tickets store next to the current repo
  --store,store path to initialize (used with --clone)
  --clone,name=path clone mapping; repeatable
flags{ticket}[4]{flag,usage}:
  --title,ticket title (required)
  --body,ticket body
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{solve}[6]{flag,usage}:
  --yes,skip the interactive publish confirm
  --answer,"answer a pending question: --answer <qid> <text>"
  --backend,"session backend: fake, headless, or herdr"
  --scenario,scenario dir for the fake backend
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{run}[5]{flag,usage}:
  --answer,"answer a pending question: --answer <qid> <text>"
  --backend,"session backend: fake, headless, or herdr"
  --scenario,scenario dir for the fake backend
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{requeue}[3]{flag,usage}:
  --from-brief-diff,requeue slices whose brief section hash changed
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{gate}[7]{flag,usage}:
  --early,gate before the frontier is fully green
  --branch,validate this branch instead of jig/<ticket>
  --doc,"brief doc path, used together with --branch"
  --pr,pr number (not implemented in v0.1)
  --scenario,scenario dir for the fake gate source
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{publish}[3]{flag,usage}:
  --yes,skip the interactive confirm
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{status}[2]{flag,usage}:
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{validate}[2]{flag,usage}:
  --store,explicit store path
  --project,"project name, resolved via the machine mapping"
flags{version}[0]{flag,usage}:
flags{skills}[2]{flag,usage}:
  --project,install under ./.claude/skills of the current directory
  --dest,install under <dir>/<name>/SKILL.md instead of the default location
flags{_screen}[0]{flag,usage}:
help[3]:
  jig run JIG-1 --backend fake --scenario ./scenario
  jig gate JIG-1 --early
  jig publish JIG-1 --yes
```

## Status rendering

`jig status <ticket>` prints the slice/question table straight from the
store - no separate dashboard. Real output from the test fixture, mid-gate:

```
ticket: JIG-1
state: building
slices[5]{id,state,attempts,blocked_by,question}:
  a,green,1,-,-
  b,green,2,a,-
  c,green,2,-,-
  d,green,1,-,-
  fix-1,queued,0,-,-
questions[1]{id,slice,status}:
  q-001,c,answered
help[1]:
  Run `jig run JIG-1` to work the frontier
```

## Proof

```sh
go build ./... && go test ./...
```

23 packages, 175 test functions. The deterministic end-to-end fixture (a
fake session backend, no API calls) runs the full brief-to-PR chain twice in
about three minutes, asserting the second run lands on the same result as
the first. The safety screens, the outcome parser, staircase model
selection, stall detection, and the store's lock-file race each carry a
table-driven test ported from the original spec.

## Session backends

A build or gate session runs against one of three backends: `fake` replays a
scripted scenario with no network calls (the CI and fixture path), `headless`
drives a local `claude -p` subprocess, and `herdr` drives a remote agent
through herdr, exec'd natively off Windows and, on Windows, inside a WSL
login shell (`JIG_WSL_DISTRO` picks the distro). All three read the same
`slice.json` and write the same `result.json` - see [ARCHITECTURE.md](./ARCHITECTURE.md#session-backends).

Every session backend that can run tools is wrapped by a structural command
screen and a secret-read screen; the binary itself pushes only at publish's
confirmed step, and refuses to push to a non-local remote without one.

## v0.1 scope

Shipped: init, ticket, run, requeue, gate, publish, solve, status, validate,
version, skills, the fake/headless/herdr backends, and the local/github
tracker adapters.
Jira/Linear tracker adapters, gate's `--pr` mode, the `fleet`/`retro` verbs,
and design oracles ship in v0.2.
