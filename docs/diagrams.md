# Diagrams

Five pictures of jig, for people who want the shape before the prose.
[CONTEXT.md](../CONTEXT.md) and [ARCHITECTURE.md](../ARCHITECTURE.md) stay
the authority: when a diagram and the prose disagree, the prose is right and
the diagram is stale. Each diagram was drawn from the code at `cc02c78`.

Each also has an [interactive version](https://develdeco.github.io/jig/)
where every element links to the code it was drawn from. The Mermaid here is
the one kept in step with the code; the interactive pages are redrawn from
their sources on the `gh-pages` branch when they drift.

- [System context](#system-context): what jig attaches to and where its state lives
- [Domain model](#domain-model): how charts, tickets, briefs, slices and rounds relate
- [A ticket from brief to PR](#a-ticket-from-brief-to-pr): the four phases and who acts in each
- [A commit's verification](#a-commits-verification): from `git commit` to the note the session hears
- [A slice's states](#a-slices-states): what moves a slice between queued and green

## System context

jig is one binary reached through four doors. It never starts the harness
session; it observes it, verifies its commits in a detached worker, and
keeps the facts in the store and the jig home.
[Interactive version](https://develdeco.github.io/jig/diagrams/system-context.html).

```mermaid
flowchart TB
  person(["Person"])
  harness["Harness session<br/>own model, tools, subagents<br/>jig never starts it"]
  clone[("Person's clone<br/>branch id-slug")]
  subgraph jigBox ["jig binary"]
    doors{{"Doors<br/>hooks, skills, MCP, CLI"}}
    verbs["jig verbs<br/>claim, validate, gate, publish"]
    hook["post-commit hook<br/>global, or a block in the clone's"]
    verify["jig verify<br/>detached oracle run"]
  end
  store[("Store repo<br/>tickets, charts, journals")]
  home[("jig home<br/>ledger, logs, evidence")]
  github(["GitHub<br/>issues, board, PRs"])

  person -->|prompts| harness
  harness -->|events, calls| doors
  doors -.->|context, notes| harness
  doors -->|observe, verbs| verbs
  verbs -->|checkpoints| store
  verbs -->|ledger lines| home
  store -.->|"mirror: issues, board"| github
  verbs -->|"publish: push head, open PR"| github
  harness -->|edits, commits| clone
  clone -->|commit| hook
  hook -->|spawns| verify
  verbs -.->|HEAD moved| verify
  verify -->|oracle, verified| store
```

## Domain model

A chart graduates into tickets. A ticket holds its brief, its slices, its
journal and its gate rounds. A slice cites the brief sections it comes from,
and a fix finding from review becomes a new slice.
[Interactive version](https://develdeco.github.io/jig/diagrams/domain-model.html).

```mermaid
classDiagram
  direction TB
  class Store {
    charts
    tickets
    claims log: minted ids, drivers
    mint(key) TicketId
  }
  class Chart {
    map.md, for people
    tickets.yaml entries
    entry blocked_by refs
    graduate() Ticket[]
  }
  class Ticket {
    id KEY-N, aliases
    title, slug
    branch id-slug
    rank
    closed: reason, by, at
  }
  class Dependency {
    <<record>>
    ticket: the blocker
    kind: merged or stacked
  }
  class Brief {
    sections: heading + sha256
    illustration: optional html
    approval: pinned to sha256
  }
  class Slice {
    goal
    workspace: repo/ws
    oracle: proves it done
    env: an env class, optional
    blocked_by: Slice[]
    from_brief: section hashes
    from_gate: round of a fix slice
    state
  }
  class Manifest {
    <<record>>
    the repo's, read at the commit
    oracles: name to command
    envs: up, check, down
    timeout
  }
  class Workspace {
    <<record>>
    id, path
  }
  class Session {
    id, harness, model
    ticket, slice
    claim(slice)
    commit(Jig-Slice trailer)
  }
  class Verification {
    key: commit, slice
    outcome: pass, fail or error
    recordings
  }
  class Journal {
    build: claim, oracle, verified
    review: gate round lines
    publish: pr, publish-done
    derive() Phase
    changelog() markdown
  }
  class Phase {
    <<enumeration>>
    brief
    build
    review
    publish
  }
  class GateRound {
    round N
    range: base..head
    reviewer: a fresh session
    intent: brief, explicit, inferred or none
    verdict: clean, findings or not-independent
  }
  class Finding {
    file:line
    action: fix, ask or note
    risk: low, medium or high
    triage: keep or dismiss
  }
  class PullRequest {
    <<record>>
    head: reviewed, pushed as is
    body: Demo if recordings were picked
    changelog from the journal
  }

  Store *-- Chart : holds
  Store *-- Ticket : holds, mints id
  Chart ..> Ticket : graduate mints
  Chart ..> Dependency : blocked_by becomes
  Ticket *-- Journal : records in
  Journal ..> Phase : derives
  Ticket *-- Dependency : blocked_by
  Ticket *-- Brief : brief.md
  Ticket *-- Slice : slices.yaml
  Ticket *-- GateRound : gate rounds
  Slice ..> Brief : cites sections
  Session --> Slice : claims, commits
  Verification ..> Slice : proves green
  Slice --> Workspace : builds in
  Manifest *-- Workspace : declares
  Verification ..> Manifest : runs oracle of
  GateRound *-- Finding : raises
  Finding ..> Slice : fix queues
  PullRequest ..> GateRound : ships clean head
```

## A ticket from brief to PR

Each phase starts from a prompt ("Plan T", "Build T", "Review T",
"Publish T"); a prompt that names several runs them in order. Confirming the
brief and approving the publish stay the person's unless the prompt
delegates them. Review has no back-edges: a fix finding becomes new work.
[Interactive version](https://develdeco.github.io/jig/diagrams/ticket-flow.html).

```mermaid
flowchart TB
  subgraph brief ["Brief"]
    ask(["Person: Plan T"]) --> intake["Session: intake<br/>recall, draft brief and slices"]
    intake --> confirm{{"Person confirms the brief"}}
    confirm -->|"valid: yes"| validate["jig validate<br/>records the approval"]
  end
  subgraph build ["Build"]
    claim["Session: claim, build<br/>commit with Jig-Slice"] -->|commit| verify["jig verify<br/>oracle per commit"]
    verify -.->|"red: note"| claim
  end
  subgraph review ["Review"]
    gate["Round opens<br/>jig gate or review context"] --> round["Fresh reviewer<br/>findings, review done"]
    round --> route{"Route findings"}
    route -->|ask| triage{{"Person triages<br/>keep or dismiss"}}
    triage -.->|"next round applies it"| gate
  end
  subgraph publish ["Publish"]
    pbody["jig publish --body<br/>renders the PR body"] --> approve{{"Person approves<br/>the reviewed head"}}
    approve --> pub["jig publish<br/>push head, open PR"]
  end
  validate -->|"Build T"| claim
  verify -->|"all green: Review T"| gate
  route -->|"fix: new slices"| claim
  route -->|"clean: Publish T"| pbody
```

## A commit's verification

A commit that names a slice is verified in the background, in a detached
worktree at that commit. The session is told the outcome once, at its next
tool call or prompt. The worker shares no memory with the session: the store
and the machine ledger are the whole interface.
[Interactive version](https://develdeco.github.io/jig/diagrams/commit-verification.html).

```mermaid
sequenceDiagram
  participant S as Session
  participant G as git
  participant O as jig observe
  participant W as verify worker
  participant T as Worktree at the sha
  participant J as Store journal

  S->>G: git commit (Jig-Slice: s)
  G->>O: jig githook post-commit
  O-)W: spawn detached
  Note over O,W: one run per commit and slice
  O-->>G: exit 0, one line only if something failed
  G-->>S: commit done

  W->>J: verify-pending, pushed first
  W->>T: git worktree add --detach
  W->>T: env up, run the manifest's oracle
  T-->>W: pass or fail
  W->>J: oracle, and verified on a pass
  Note over W,J: pass: the slice is green<br/>fail: building, unless it needs input<br/>cannot run: verify-error

  S->>O: next tool call (hook)
  O->>J: read journal
  J-->>O: outcomes since the session began
  O-->>S: note, told once
  Note over S,O: a pass is only told<br/>a fail or verify-error also holds Stop<br/>when the claim is unattended
```

## A slice's states

A slice is ready when it is queued and every slice it is blocked by is
green; `jig next` lists those. Only a verification makes a slice green.
[Interactive version](https://develdeco.github.io/jig/diagrams/slice-states.html).

```mermaid
stateDiagram-v2
  [*] --> queued
  queued --> building : jig claim
  building --> green : oracle passes
  queued --> needs_input : jig ask
  building --> needs_input : jig ask
  building --> stalled : same failure twice, unattended
  building --> env_blocked : env class will not come up
  building --> queued : jig release
  green --> building : a later commit fails
  green --> queued : jig requeue --from-brief-diff
  needs_input --> building : answered, still claimed
  needs_input --> queued : answered, unclaimed
  stalled --> queued : jig requeue
  env_blocked --> queued : jig requeue

  state "needs-input" as needs_input
  state "env-blocked" as env_blocked
```

The diagram draws each move from the state it usually leaves. The code is
wider:

- A verification moves a slice from any state. A pass makes it green and
  clears its question; a fail makes it building, unless it needs input. An
  outcome for an older commit that lands after a newer one's leaves the
  state alone.
- An env class that will not come up blocks a slice from any state.
- `jig claim` also moves a stalled or env-blocked slice to building, and
  `jig release` also queues one.
- `jig requeue --from-brief-diff` queues every slice that cites a changed
  brief section, whatever its state, and supersedes its open question.
