# A ticket has a kind, and a chart is one issue's plan

**Superseded in part by [ADR 0062](0062-a-container-publishes-once-its-children-have-landed.md):** `jig next <T>` of an issue or a multi-task now says where its own pull request stands (`waiting`, `ready` with the prompt `Publish <T>`, `published`) and `jig publish <T> --yes` opens it once its children have landed, so "no prompt" and "no pull request of their own" no longer hold. The kinds, the parent link and the chart as one issue's plan stand.

A ticket was one thing, a unit of work with a brief, slices and a pull request, and a chart was the map over many of them. Work that starts as a need (a person's issue) has no home in that shape: the need is not buildable, and several tickets meet it. The store needs the need, the tickets that meet it, and the groups among them, without a second record type and without rewriting the 205 tickets already in it.

## Decision

**A ticket has a kind.** `ticket.yaml` gains `kind:`, one of `issue`, `task` or `multi-task` (`store.Ticket.Kind`; `KindOrDefault` is the resolved kind). An absent `kind` reads as `task`, and the file writes `kind` only when it is not `task`, so no store is rewritten and a ticket recorded before the field is a task. A value outside the three fails the read as `TICKET_RECORD_INVALID`, and `jig validate <T>` reports it. `jig ticket new --kind` mints one; any other value is refused.

**A task may have a parent.** `ticket.yaml` gains `parent:`, the id of the multi-task a task sits under (`store.Ticket.Parent`). `jig validate <T>` reports a parent that is not a ticket of kind `multi-task` and a parent on a ticket that is not a task. A multi-task holds tasks only.

**A chart is one issue's plan.** `charts/<name>/tickets.yaml` gains a top-level `issue:` (`store.ChartFile.Issue`), and an entry gains `kind:` (`task` or `multi-task`) and `under:` (`#k`, the 1-based position of a multi-task entry of the chart, or that entry's ticket id). The issue is never an entry: the entries are the tickets that meet the need. An issue has one chart and a chart one issue (`jig chart new --issue` refuses an issue that has a chart); `store.ChartIssue`, `ChartOfIssue` and `IssuesOf` answer. `jig validate <chart>` lists the problems of the issue and of the kinds, after the entry checks and before the ref checks, so `jig graduate` refuses the first the same way. `jig graduate` mints each ticket with its kind, the multi-task first, and each task under one with its `parent`; an existing entry keeps its ticket's kind, and a disagreement is a listed problem.

**The need's text lives in the issue ticket's `body`.** The chart's folder stays `charts/<name>/`; a chart may be named after its issue, and nothing resolves an issue id to a chart yet.

**A ticket that is not a task is not briefed or built.** An issue and a multi-task have no brief, slices or pull request of their own at this stage. `jig next <T>` and `jig status <T>` print the kind, the chart or issues, and the children, with no prompt; `jig next` lists such a ticket by its kind and never as ready.

## Why the issue is not recorded on a ticket

A ticket's issue is the one whose chart lists it. A task that two charts list has two issues, and a field on the ticket could only name one, or would have to become a list the charts already are. Reading it from the charts keeps one source and costs a scan of the chart files that mention the ticket's id, which `IssuesOf` limits to those files (it runs at every hook event). A caller that needs one container decides among the issues it gets.

## Legacy charts are not migrated

A chart with no `issue:` is a legacy chart. It validates and graduates exactly as before, and its tickets read as tasks with no parent. A chart that sets `issue:`, or an entry's `kind` or `under`, is on the model and is checked by it. Moving the existing charts onto the model is not done here: it is a decision per chart about which need it plans.

## Consequences

The tracker's copy of a ticket keeps the word issue only as **Tracker item** in the glossary, so the store's issue and the tracker's never mean each other. How an issue's pull requests, the suite run for a multi-task, and the tracker mirror treat the new kinds are later tickets of the chart that made this one. A chart run does not yet leave a multi-task or an issue entry out of its briefs.
