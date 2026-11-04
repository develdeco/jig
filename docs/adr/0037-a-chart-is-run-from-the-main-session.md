# A chart is run from the main session, with one build subagent per ticket

[ADR 0033](0033-phases-are-asked-for-by-prompt.md) made a ticket's phases something the person asks for by prompt, and [ADR 0035](0035-start-to-finish-and-recommendations-are-separate.md) kept the person's moments theirs in a run. Both are about one ticket. Work that spans tickets was a chart, and the chart had no run: intake could write one and `jig graduate` could mint its tickets, and from there a person asked for each ticket by hand.

The owner walked a harness session beside `jig tui` through the whole flow in a mockup session (2026-10-29, six frames) and approved its shape. It showed four gaps in what jig did that day. Intake minted a ticket before it knew whether the work was one ticket, so a chart verdict left an empty ticket behind that `jig next` listed as `new`. A prompt like "Take it from brief to PR" that turned out to be a chart of four tickets had no rule: the session could stop silently or quietly start four tickets the prompt never named. Nothing told a session where a chart stood, so the tickets behind a `merged` dependency could not be told from the ones ready now. And the hooks saw a session and its subagents as one: a build subagent's `--unattended` claim held the main session at its Stop, a subagent's stop was never seen at all, and a note told once to "the session" reached whichever agent ran the next tool call, so the main session that relayed an answer was told its own relay.

## Decision

**The verdict comes before the mint.** Intake recalls and grills first and mints nothing. A brief verdict mints the ticket with `jig ticket new` and drafts it; a chart verdict mints none and writes `charts/<name>/`, whose tickets `jig graduate` mints. A request that turns out to be a chart no longer leaves a ticket behind.

**A chart verdict ends a ticket run, and the session asks.** A prompt that named one ticket's phases ("Take it from brief to PR") handed over one ticket; widening it to a chart of N is a scope change the prompt did not hand over. The session says why, reports what graduated, which tickets are ready and what waits on a merge or a stack, and, attended, asks whether to take the chart's tickets on in this session, with the recommendation: brief them all now, then build the reachable ones to PR. With "go with your recommendations", or unattended, it stops and only reports. This is skill text, as ADR 0035's run modes are.

**A chart run is a prompt aimed at a chart, run from the main session.** "Take <chart> to PR" when nothing of the chart started, "Continue <chart>" when something did, or a yes to the verdict's question. The main session does everything that needs the person: it briefs every ticket up front (one grilling round across the chart, one confirmation, `jig validate` for each), asks every question, decides the review's asks with `jig triage` and asks the publish approvals together. Builds run in waves, one subagent per ticket, each in its own worktree on the ticket's branch. A build subagent claims its slices `--unattended`, asks with `jig ask` and then hands back to the main session, which asks the person, relays the answer with `jig answer` (recorded as relayed, agent provenance) and resumes the subagent, and spawns its own reviewer subagent once every slice is green. The run ends at a ticket behind a `merged` dependency, saying what is published and what waits on which pull request; "Continue <chart>" resumes it once the pull request merged. The `chart` skill carries this, and it is a project skill.

**Notes are told once to each session and agent, and a subagent's stop is its own.** Claude Code's `SubagentStop` is installed and read as a `stop` with `Agent` set (never as the main session's stop). The holder of a claim is the agent that ran the `jig claim` call, read from the ledger line the PostToolUse hook wrote for it, so a subagent's `--unattended` claim holds that subagent at its stop and never the main session, each agent of a session by its own latest claim (ADR 0044); where the holder is unknown (a harness with no hooks), the whole session is the holder, as before. A `note-delivered` line carries the agent, an answer note goes to the agent that asked, a ref that two tickets could share names its ticket, and `spend` stays off a subagent's stop, whose transcript is the whole session's.

**The chart's standing is derived, and `jig next <chart>` says it.** `phase.DeriveChart` is pure like `phase.Derive`: from the entries' needs and their tickets' phases it gives each ticket's wave and reach (done, here, after a merge, blocked), the tickets to brief, the build waves that can run now, where the run ends and the pull requests it waits on, whether the chart started, and the verdict with its prompt. `jig next <chart>`, `jig status <chart>` and the TUI read that one function through verbs. Nothing records a chart run: ADR 0033's rejected "record of a run's span" still holds, because the span is the prompt and the standing is in the lines the tickets already wrote. `map.md` is read for it (its sections), never written.

**The TUI shows the run.** The project screen lists the charts after the tickets, and a chart opens a screen with the map, the tickets with their four step marks and what each waits for, the run and the tickets' activity together. It reads `jig next <chart> --json` through the verbs, not the store.

## Rejected alternatives

- **A record of the chart run** (a `chart-run` line, or a run file under the chart): a second place that says what the person asked for, one that outlives the prompt. ADR 0033 rejected the same thing for a ticket's run, and the derived standing answers "where is it" without it: a chart that started has a graduated ticket past its brief, and a chart that is waiting is waiting on a pull request the host can say about.
- **A `jig chart run` that starts the subagents.** jig starts no coding session and links no session code (`lint/deps_test.go`, `TestCLIDoesNotLinkSession`); a verb that launched build agents would bring back the dispatched path ADR 0032 retired. The session starts its own subagents, and jig records and verifies what they do.
- **One session per ticket.** Each ticket would have its own session and nothing to tell apart in the hooks, but every question, approval and relay would reach the person from N sessions: the person would be the scheduler. A subagent per ticket keeps the person talking to one session.
- **A ticket-level `jig ask` for the brief's questions.** A chart's briefs are decided in one grilling round across the chart, since the questions overlap; a question parked on a ticket that has no slice yet would be asked once for each ticket, and the answers could disagree.

## Consequences

- Amends [ADR 0033](0033-phases-are-asked-for-by-prompt.md): decision 37 (a run of several phases is the prompt) gains a run aimed at a chart, and decision 39 no longer holds that a subagent and its parent are one agent to the hooks, since a note and a hold now follow the agent id (a review's independence still follows who ran `jig review context`, and the main session still never reads the round). The planner no longer mints first (DECISIONS.md, "The reviewer subagent runs in the foreground, and the planner mints its ticket", is superseded in its minting half): the TUI, which tied the drafting to the ticket at the mint, now opens on the ticket after the grilling, not before.
- Amends [ADR 0035](0035-start-to-finish-and-recommendations-are-separate.md): a chart verdict ends a start-to-finish run, and the same person's moments stay theirs in a chart run unless the prompt hands them over.
- `CONTEXT.md` gains *Chart run* and *Chart standing* and corrects *Chart* (jig reads `map.md`'s sections), *Intake*, *Start-to-finish run*, *Note* and *Unattended*.
- `ARCHITECTURE.md` changes in the hooks (a `SubagentStop`), notes (per session and agent, the claim holder), the TUI (eight screens), `internal/phase`, `internal/recall`, `internal/ledger` and `internal/observe` rows, and the skills paragraph.
- A project that attached jig before this has no `SubagentStop` entry until it runs `jig attach` or `jig hooks` again; `jig doctor` lists the missing entry, and until then a build subagent is never held at its stop.
- The skills stay under the 6000 bytes and 100 lines the lint allows: the new `chart` skill is its own, and intake gave up a few sentences to make room.

## Amendment: a screen that waits on a merge is read again on a timer (GRAPH-15)

The chart screen was read again on open, on `r`, and after the screen had been quiet since an event of one of the chart's tickets. A merge on the host writes no line to any journal, so a chart that ended at a pull request showed "waits for a merge" until a person pressed `r` or something happened to one of its tickets. The owner decided that a screen that waits on a merge is read again on a timer, every two minutes (chart flow, C14).

- **The rule.** A screen waits on a merge when it shows a wait that only the host can end. The TUI reads that screen again every `Config.MergeEvery`, and a screen that waits on nothing is not read on the timer.
- **Which screens wait.** The chart screen waits when the last read that printed a chart listed at least one entry in `ends`, the tickets the run stops at, each behind a merge. A read that printed no chart leaves that as it was, so one blank read does not stop the timer. A ticket's screens (ticket, brief, build, review, publish, slice) wait when the ticket is published with a pull request on record and is not closed. The project's list waits on nothing, an unpublished ticket waits on nothing, and a closed ticket waits on nothing.
- **What a tick reads.** Only the screen on top, through the verbs the screens already read. A chart screen that waits reads the ticket list and the chart (`jig next --json` and `jig next <chart> --json`), the two reads the idle read makes. The statuses of the chart's tickets are not read, since a merge changes no status. A ticket screen that waits reads that ticket's status, which shows a closure made in another session. A read already out is not doubled, a read that fails is retried as before, and a tick moves no cursor and sets no notice.
- **The field and the flag.** `Config.MergeEvery` is a duration: zero is two minutes, and a negative value never reads again, as `AwaitEvery` does, for a test that settles `Init`. The timer starts at `Init` and runs for the life of the model, whichever screen is on top. `jig tui --merge-every <duration>` sets it, so an end-to-end test can set it short and a person can see it in `jig tui -h`. A negative value is refused as `VALIDATION_ERROR` before any screen; turning the timer off is not offered to a person.

Rejected alternatives:

- **jig or the TUI fetching the clone on the tick.** A merge judged by ancestry (a ticket published with no pull request on record) shows once `refs/remotes/origin/<target>` holds the branch, and only a fetch moves it. A fetch from a screen is a network call and a write to the person's clone that nothing in jig makes today, and the screens read no store and call no host.
- **A host watcher or a webhook.** Both need something to run beside the TUI, or a public address to call, for a state `jig next <chart>` already asks the host for.
- **Reading every screen on the timer.** Most screens show nothing a merge changes. The timer would read the roster and a status every two minutes for each one, and the cost would show nowhere.

Consequences:

- The chart verb asks the host about the pull requests it names on every read, so a merge shows in the next tick's read. The listing `jig next --json`, which the tick also reads, may take an open pull request's state from this machine's memory when it is under two minutes old, so a blocker in the listing can read an open pull request up to two minutes late.
- A merge judged by ancestry shows once something fetched the clone: a person's `git fetch`, or a pull. The timer does not fetch.
- A ticket's screens show no host state of their own pull request, since no verb they read prints one. The tick changes what a ticket screen shows only when its status changed.
- `CONTEXT.md` (*Chart standing*), `ARCHITECTURE.md` (the `jig tui` paragraph and the `internal/tui/` row), the README and `DECISIONS.md` say the same.

## Amendment: the chart screen's activity includes the chart's store history (GRAPH-16)

A chart has no journal, so the chart screen's Activity panel showed its tickets' lines and nothing else. A map edit or a graduation leaves no ticket line: a chart whose map was just edited, or that was just graduated, read "nothing yet". The store is a git repo, and the commits that touched `charts/<name>/` are the chart's own history. The owner decided the chart screen shows them as part of its activity (chart flow, C13).

- **The history.** `jig next <chart> --json` gains a `history` key: the commits of the store's `HEAD` that changed anything under `charts/<name>/`, as git's path-limited log gives them, newest first, each as `{sha, time, subject}` with the committer time in UTC. It is the 20 newest (`chartHistoryLimit`), always present, and `[]` when it cannot be read (the store is not a git repository, it has no commit, git fails): a problem is a row, never a reason to hide the chart. Only `--json` reads it. The text of `jig next` and `jig status` is the session's, which has no use for it, and starts no extra git process.
- **One git call.** `gitx.PathLog` runs one `git log` with the paths taken literally. Path-limited history with git's merge simplification is what the git program computes, and `internal/gitx/read.go` keeps history walks on it. `Store.ChartHistory` asks it for the chart's folder, which `Store.ChartRelDir` names in one place beside `ChartFile`.
- **On the screen.** `parseChart` reads `history` into the chart's info (a missing key is none, an entry whose time does not parse is left out). The Activity panel adds each commit to the tickets' lines, oldest first, and sorts the whole list by instant with a stable sort, so two commits of one second keep git's order and a commit stands above a ticket's line of the same second. A commit is drawn `HH:MM:SS chart  store  · <sha7> <subject>`, and the panel's count and its "nothing yet" take commits into account. With no history the panel is drawn as it was.
- **Fresh as the read.** The history is as fresh as the chart's last read: on open, on `r`, when a graduation changes the listing, and after a quiet that follows a touch of one of its tickets. This adds no trigger and no timer.

Rejected alternatives:

- **A chart journal.** A file the chart's actions append to would record the run, which decision 4 of the chart's map rejected, and would be a second copy of what git holds.
- **The TUI reading the store itself.** `TestTheScreensReadNoStore` forbids the screens importing `store`, `project` and `gitx`: they learn everything from a verb's output, and this stays one.
- **An in-process log walk in `internal/gitx`.** Merge simplification and the path filter are git's to compute, and a second implementation would drift from it.

Consequences:

- Every JSON read of a chart starts one `git log` in the store clone, so the git budgets of the packages whose tests read one (`.github/git-budget.yaml`) move up.
- The Activity shows the time of day of an old commit, as it does for a ticket's line, with no date.
- `CONTEXT.md` (*Chart history*), `ARCHITECTURE.md` (the chart paragraph and the `internal/gitx/`, `internal/store/` and `internal/tui/` rows) and `DECISIONS.md` say the same.

## Amendment: the chart screen details the ticket under the cursor (2026-11-02)

The chart screen drew the same Run and Activity panels whichever ticket the cursor stood on. A run builds each ticket with a subagent of its own, so the person asked who holds a slice, which agent claimed it, how far it has come and whether that agent was held at its stop, and had to open the ticket to see. The owner decided the chart screen details the ticket under the cursor from what the ticket screens already read (chart flow, C10). What the owner left open was taken at the recommended answer (2026-11-02, proceed with the plan).

- **The panel.** A Detail panel sits between the Tickets and the Run panels, and its top edge ends with the id of the ticket under the cursor. It is drawn whenever the chart has a ticket row, and not for a chart of fog entries only. Its rows are the Run panel's rows (label, text, weight), never rows the cursor can stand on, so what the cursor's index means does not change. It has four rows at most (`detailMax`).
- **No new read.** The data is the stream and `jig status <T>`, which the ticket screens read. There is no new verb, no new field in any JSON and no new read. Moving the cursor draws the panel again and runs nothing.
- **Slices.** One row for each slice the build has under way: a sub-step of the build whose key starts with `slice:` and whose state is active, needs you or failed. The row is the slice id, its stage (claimed, committed, verifying or verified while it is under way, "waits for your answer" or "stalled" otherwise), the session that holds its claim, named as the activity names it ("builder"), and the agent that claimed it. A slice has a holder only while its claim is live: its latest `claim` line, not followed by a `release` of the slice.
- **The agent.** A claim line has no agent, because the CLI cannot know which agent runs it. The agent is read from the machine ledger as `claimHolder` reads it: the tool line of the claim's session that ran `jig claim` and not `--release`, the nearest in time to the claim line, stamped no more than a minute before it. When no such line is kept (the harness has no hooks, or the line fell out of the ledger lines the screens keep) there is no agent, and the row still names the holder. The screens may not import the ledger, so `claimCall` in `internal/tui` mirrors `ledger.RunsJig` for the claim verb and the `--release` refusal, and a test that may import both pins that they agree.
- **A current hold.** A hold is a ledger line that says jig held an agent at its stop. It is current while no journal line that belongs to a step follows it: a verification landing, a gate opening or a pull request recorded ends it. A `spend` line belongs to no step, so the main session's own stop does not end a hold. The panel shows the newest two current holds, oldest first, after the slice rows, worded as the activity log words them ("the session" when no agent made the stop). The log keeps the history of the ones that ended.
- **Nothing to detail.** A ticket with no slice under way and no hold has one dim `state` row: the words of the ticket's header, or "not read yet" for a ticket the screens do not know. The panel is there for every ticket, so the layout does not jump as the cursor moves.
- **Rows given up.** A short screen gives rows up in this order: the Map, then the Run panel's rows down to one, then the Detail panel's rows down to one. The Tickets panel is never cut and the Activity panel keeps its floor. A hold weighs less than a slice row, so a slice row goes before a hold. `chartPage` counts the Detail panel with its frame before it cuts, and reads the cursor as the view clamps it, never through `selected()` or `page()`, which call it.

Rejected alternatives:

- **The agent on the claim line.** `jig claim` cannot know which agent runs it, and the hook that hears the call is what writes the agent, on the ledger's line.
- **The harness and model of the holder.** A subagent runs on a model of its own, and the session's would mislead.
- **A verb for the detail.** The ticket screens already read what the panel shows, so a verb would add a read for nothing.
- **The ticket's worktree on the panel.** That is advice `jig next` does not give on this branch, and the panel reads nothing new.

Consequences:

- On an 80 by 24 screen the map line and the Run panel's waves give way to the panel, and a taller screen keeps them.
- A harness with no hooks shows the holder and never the agent, and a hold only the hooks write is never shown.
- `CONTEXT.md` (*Chart detail*), `ARCHITECTURE.md` (the `jig tui` paragraph and the `internal/tui/` row), the README and `DECISIONS.md` say the same.

## Amendment: a subagent drafts each brief and publishes each ticket (2026-11-03)

A chart run kept the briefs and the publish work in the main session. The main session ran out of context once, in the publish work (rendering bodies, picking demos, keeping each pull request within the CI budgets), not while briefing, and it had spent a share of its context on seven briefs. The owner decided the `chart` skill hands both to subagents (chart run, C16, map decision 30). What the owner left open was taken at the recommended answer (2026-11-03, take them, continue).

- **A drafter per ticket.** The main session starts one subagent per ticket (Agent tool, `model: sonnet`, in the background) that drafts `brief.md` and `slices.yaml` with the intake skill, takes the recommended answer for every question the ticket leaves open and marks it, checks the files with `jig validate <T> --check` until it prints `valid: yes`, and hands back its questions with their defaults. Meanwhile the main session asks the person one numbered round across the tickets, records the answers, sends a changed answer to its drafter (SendMessage) to redraft, confirms once and runs `jig validate <T>` for each. The person's moments stay theirs (ADR 0035).
- **A publisher per ticket.** After the one yes for the group (a prompt to go with recommendations needs none) the main session starts one subagent per ticket that follows `jig skill publish`: it renders the body, picks the demo, keeps the pull request within the budgets CI asks, opens it, records it and hands back the url. It asks the person nothing: a question it cannot settle ends its turn with the question and its default. The main session keeps the waves, the questions, the approvals and the removal of the worktrees, one at a time.

Rejected alternatives:

- **The drafting stays in the main session.** It spent its context on seven briefs, and the questions wait on the drafts.
- **One subagent for all the briefs.** It cannot take a changed answer for one ticket without redrafting the rest.
- **The publish in the build subagent.** It is held at its stop and has no round to publish in.

Consequences:

- The main session keeps the run: the waves, the questions and the approvals. Drafters and publishers are Sonnet subagents with no approval of their own.
- The skill text grew by at most 150 net bytes, inside the budget the chart skill shares with ADRs 0044 and 0045 (6000 bytes and 100 lines).
- A publisher's `gh` and push may meet a permission prompt the one yes for the builds does not cover: the group's yes is the place to say it.
- `CONTEXT.md` (*Chart run*), `ARCHITECTURE.md` (the Phases paragraph) and `DECISIONS.md` say the same.
