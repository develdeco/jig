# A container publishes once its children have landed, with no round

A task's pull request goes into its container's branch (ADR 0058) and is merged by the session at the person's word (ADR 0061). The multi-task's and the issue's own pull requests were left unopened: nothing said when they were ready, and publish needs a reviewed head and a gate round that a container, which builds nothing itself, never has.

## Decision

**A container is ready when its children have landed.** `land.ReadinessOf` reads it, through the same merge fact the Land rules read: a task by its merge into this container, a multi-task by its own pull request, a shared task by the pull request into this issue. `jig next <multi-task|issue>` says `waiting` with one row per child not landed (its standing: `published`, `building`, or why it is closed), then `ready` with the prompt `Publish <T>`, then `published` with `Land <T>`. A container with no children is not ready.

**One seam for what else it waits for.** `land.Preconditions` is a registry of `func(Container) ([]Unmet, error)`, empty here; `land.Check` runs it, and an unmet precondition holds `ready` and the publish with its own code and prompt. A later ticket adds an entry, and nothing else changes.

**Publish opens it with no round.** `jig publish <T> --yes` of a multi-task opens its pull request into its issue's branch, of an issue into the target, one per repo its tasks build in, linked as a task's are. Every line was reviewed in its task, so no gate round, slice, review or demo precedes it and none is made. It writes `pr-body`, `publish-approved` (the person's word, an agent's record when the session ran it), `pr` and `publish-done`, refuses `PUBLISH_NOT_READY` (or a precondition's code) before any write, and pushes nothing: the container's branch is the base its first child's pull request went into, and one the host lacks is the host's refusal. The body is short: the title, an issue's need, one line per child with the pull request it landed, a multi-task's tasks nested, and the project's trailer.

**The merge fact of a container** reads its own recorded pull requests, in the union of the repos its tasks build in (every repo while it has none placed), with the branch-tip fallback against its base, as a task's does.

**A task cannot wait for its own container.** The container publishes after its tasks land, so a `blocked_by` that names it never ends: `jig validate` of a chart (and `jig graduate`) and of a ticket list it as a problem.

## Consequences

A chart run still ends at its tasks' pull requests; the containers' are opened by the session at the person's word, then landed (ADR 0061). The standing `merged on the host, not recorded` is not a wait: a child merged on the host counts as landed, as it does for `jig land`.
