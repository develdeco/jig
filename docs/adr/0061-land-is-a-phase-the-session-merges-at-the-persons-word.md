# Land is a phase: the session merges at the person's word and jig records it

A ticket's pull request merged by someone's hand, outside jig, and jig learned of it only by asking the host (the merge fact). With containers (ADR 0057, 0058) the order of merges matters: a task goes into its issue's branch, a stacked child after its parent, an issue after its tasks. Nobody owned that order, and "do not merge" was a line in each skill, which left the person to merge each pull request by hand and tell jig nothing.

## Decision

**Land is the fifth phase, after publish, for every kind of ticket.** `phase.Land` joins `phase.Names`, a ticket has five phases, and `phase.Prompt(Land, T)` is `Land T`. A published ticket whose pull request has no landed line is at Land. Its code stays `PHASE_DONE`, so every reader that keys on the code (a chart run, the hooks, the prompt notes) reads it as it always did; only its prompt and `jig next`, `jig here` and `jig status` name `Land T`. A landed ticket, one closed without a pull request and one that published none (a repo with no pull-request host) read as a published ticket did before: they have no Land to do. The TUI keeps drawing the four steps of the work (`Ticket.Work`).

**The session merges, at the person's word, and jig names and records.** "Land T" is the person's approval of the merge, and nothing else is asked of them. `jig land <T>` never merges. It refuses a landing that is out of order and writes nothing, otherwise names the merge to make in each repo (`gh pr merge <url> --squash` where the repo allows squash, else `--merge`, else `--rebase`; `--method` names another), and once the host says every pull request of the ticket merged writes one `landed` line per repo. A session that was not told to land never runs it, and a start-to-finish run, a chart run and the Stop hook end at published.

**The refusals are the order, and the order is the chart's.** `LAND_NOT_PUBLISHED` (not published, no pull request, or owed a restack or retarget), `LAND_WAITS` (a `merged` dependency, a stacked parent, a multi-task's tasks or an issue's tickets that have not landed, named so the session lands them first), `LAND_BASE_DIFFERS` (the pull request's base is not the stack plan's `PRTarget`: `jig publish` points it right) and `LAND_CLOSED` (closed without merging). Order is the `blocked_by` edges and the parent links, nothing else, in one list (`land.Rules`) so a later rule is one more entry and `jig land` does not change.

**A base that moved past a branch is no refusal.** A pull request whose base gained commits since the branch was cut is not behind: the host merges it, as it merges a ticket into the target today. Only a host that reports it `CONFLICTING` is named, with the merge that resolves it (`git merge origin/<base>` in the ticket's worktree, then `jig publish`), and a merge resolved by hand is a delta for review as a reviewed head says. A host jig cannot ask is a note, never a refusal: jig records what it cannot prove and refuses nothing for it.

**The landed line is a record, not a second source.** It carries the repo, the pull request, `base` (the branch it merged into), `into` (the container, the field a shared task's lines are told apart by, read with `journal.ReadFor`), the merge commit and a `provenance`: `agent` when a session ran `jig land` after it named the merge, `person` at a terminal, `host` for a merge made on the host and found there. A merge made on the host is noticed (`merged on the host, not recorded`) and recorded by the same verb when asked; nothing writes a line unasked, since a reader does not write. The merge fact still reads the host.

**A shared task lands per issue.** `jig land <T> --issue <ID>`, refused with `SHARED_TASK_ISSUE_NEEDED` when none is given, and one issue's landing says nothing of the other's.

**A container is complete** when its children have landed in its branch and its own pull request has (`land.Complete`); `jig land` lands it by the same rules once publish has recorded its pull request.

## Consequences

A chart run does not merge, and the chart skill's last rule becomes "merge a pull request only at the person's word". The merge is one more thing a session does for a person, with the order and the commands from jig. Whether a repo is squash, merge or rebase is read from the host, so a project needs no setting for it. What the mirror shows of a landing, and the order across issues, are later entries.
