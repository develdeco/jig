# The mirror's card never runs ahead of the issue

A GitHub Project has a built-in **Auto-close issue** workflow, on in every new project: moving an issue's card to `Done` closes the issue as completed. So a card is not display alone. [ADR 0059](0059-the-mirror-carries-the-issue-and-posts-the-plan.md) gave an issue ticket's card the Status of its chart's tickets, and its open or closed state its own ticket. Once every task was done the card read `Done` on an open issue: GitHub closed it before its work reached the target, the next sync reopened it, and it closed again later. An issue whose tasks were all cut was closed as completed the same way, and kept that reason, since the sync compared only open with closed.

## Decision

**The card never runs ahead of the issue.** A sync gives an issue's card `Done` or `Not planned` only when that sync holds the issue closed, and it writes the issue's open or closed state and its close reason before it moves the card. This holds for every item with an issue (an issue ticket, a task, a multi-task, a standalone chart), whether the issue is new, recorded, recreated or reopened, in a dry run's report as in a real sync. An item the publish-safety scan skips gets neither write. A pull request's card shows the pull request and is not covered.

**This replaces ADR 0059's rule for an issue's card.** An issue ticket that is closed or reads finished shows its own Status, and so does one with no chart. An unfinished one is `In review` while its own pull request is open, and otherwise shows what its chart's tickets say, except that their finished readings are never the issue's: where they would say `Done` it reads `In progress`, and where they would say `Not planned` it reads `Backlog`. The rest of ADR 0059 stands: the issue opens and closes with its own ticket.

**A ticket is finished only on its own record.** It is `Not planned` when closed as superseded or not planned. It is `Done` when closed as covered, when its journal says it landed, or when the host reports at least one of its pull requests merged and none still open; a pull request the host no longer resolves counts as not open. A chart whose tickets are all `Not planned` is `Not planned`, and one whose tickets are all finished, at least one `Done`, is `Done`. A chart is folded once per sync over every ticket it lists, each read on its own record, whether the mirror carries it or not, so the issue's open state and its card come from one reading.

**A closed issue takes the store's reason.** For an issue GitHub already holds closed that the store holds closed too, a reason that contradicts the store's is set to the store's without a reopen, whoever closed it. Held as completed, `not planned` and `duplicate` are corrected. Held as not planned, `completed` and no reason are corrected; `duplicate` already says not planned. The write is REST `PATCH /repos/{owner}/{repo}/issues/{number}` with the closed state and `state_reason`, the one write the mirror makes outside GraphQL: REST's update takes the reason of an issue that is already closed, and whether GraphQL's `closeIssue` changes it was not checked against GitHub. The reason is read in the batch that reads the issues. The record keeps no reason, so a reason changed on GitHub prints no drift line.

## Consequences

- jig reads and changes no project workflow. The same states and cards come out with Auto-close on or off.
- An issue's card no longer shows that all its tasks are done: it reads `In progress` until the issue's own pull request opens.
- GitHub's **Item closed** workflow moves any closed card to `Done`, also one closed as not planned. When it runs after jig's board write, the card reads `Done` until the next sync sets `Not planned` again. It changes no issue's state or reason.
