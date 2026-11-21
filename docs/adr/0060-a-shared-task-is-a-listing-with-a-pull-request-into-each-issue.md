# A shared task is a listing, with a pull request into each issue

A task can be wanted by two issues. The model (ADR 0057) puts a task under its chart's issue, and its pull request goes into that issue's branch (ADR 0058). A task two charts list has two issues, so it has two places to land. Copying it into a second ticket would build, review and merge the same change twice.

## Decision

**A shared task is a listing, not a kind.** It is a task that two or more model charts (charts that name an issue) list as an entry's id. There is no new ticket kind and no cap of two. `store.Store.ChartsListing` answers each listing; `IssuesOf` is its issues and `ContainersOf` its containers (the `parent`, else the issues). `ContainerOf` is the one element of `ContainersOf`, so a shared task has no single container, as before. A shared task starts from the target, has one branch, and goes into each issue's branch with its own pull request.

**Only a task that waits for nothing and sits directly under each issue can be shared.** `jig validate` lists, and `jig graduate` refuses with nothing minted, an entry that is also an entry of another model chart and waits on something (in either chart or on the ticket), is a multi-task, or is under a multi-task. A dependent of a shared task is fine: its `merged` edge reads the merge into its own issue.

**`into:` is the record.** The four publish lines (`pr-body`, `publish-approved`, `pr`, `publish-done`) of a ticket that has a container carry `into`, that container's id. A line with none is the one container's, so every journal written earlier reads as before. A shared task's line with none is of neither issue.

**A reader asks for an issue.** `journal.ReadFor(ticket, issue)` is the journal with the other issues' publish lines dropped, and is the whole journal for a ticket with at most one container. `stack.ReadFor`, `mergefacts.Reader.OfFor` and `verifydeliver.ReadStatusFor` are the plan, the merge fact and the publish state through it. Read for no issue a shared task is planned from the target with no pull request target and each issue in `Containers`; its merge fact is met only when every issue's is; it has published when every issue has. A chart reads its entry, and a `merged` blocker, through its own issue, so the same task is done in one chart and waiting on a merge in the other, and an issue whose copy merged does not release a dependent of the other.

**A publish names its issue.** `jig publish <T> --issue <ID>` takes the plan, the pull request (the host is asked for the one from the branch into that issue's branch, not the other's), the approval and the lines for that issue. A shared task with no or a foreign `--issue` is refused before any write (`SHARED_TASK_ISSUE_NEEDED`). The gate and the review work on the branch head once: a clean round of a head is the review of every issue's pull request of that head. Each pull request body names the other issues' pull requests (`Shared with <issue> (pull request <url>)`).

**A fix after an issue merged is a new task.** The pull request that merged is of the head its `pr` line records. When the task's branch has moved past it, `jig next` and `jig status` say the issue's row is stale and that the fix is a new task in its chart, not a push to a merged branch. An issue whose pull request is open takes the push. Nothing is refused: the record shows it.

**The squash case.** Issue A merges the task into its branch by squash, issue B does the same, and each issue then goes to the target by squash. Git applies an identical change from both sides cleanly, so the second issue's pull request to the target conflicts only when A changed the task's own lines after merging it. That shows at the second pull request, which `jig next <issue>` already calls behind, and is resolved as any task behind its base is (merge the target in; reviewed only if resolved by hand). jig detects nothing ahead of it and simulates no merge. It was reasoned, not run: the first real shared task is the check.

## Consequences

A ticket listed by one chart, by a legacy chart, or by none reads as it did. `jig next` over the whole store reads a shared blocker conservatively (held until every issue published it), and a single dependent is read exactly. The mirror's link between a shared task's issues, and the order in which Land merges them, are other entries'.
