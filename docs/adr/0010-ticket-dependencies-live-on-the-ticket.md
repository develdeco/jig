# Ticket dependencies live on the ticket, not the chart

A dependency between tickets is recorded once, in the blocked ticket's own
`<ticket>/ticket.yaml`, never in the chart that happened to create it. The
chart's `tickets.yaml` is where a dependency is first declared, but the
moment `jig graduate` mints the ticket that declaration is resolved to a
concrete ticket id and written into `ticket.yaml`; from then on the chart
entry is frozen and `ticket.yaml` is the one source a later command reads.
This follows ADR 0003 directly: the store schema is the API, and a fact
that could be read from two files - a chart entry and the ticket it minted
- is a fact with two owners the moment either one is edited alone. A chart
is a planning-time view onto tickets that do not exist yet; once they
exist, their own files own their own state, the same way `slices.yaml`
owns a ticket's slice graph rather than deferring to whatever brief asked
for it. Consistent with ADR 0005, the tracker only mirrors what the store
already decided - a github issue's dependency link is a projection of
`ticket.yaml`, never a second place a dependency could be declared or
disagree with the store.

There are exactly two kinds, `merged` and `stacked`, because they are the
two ways a child's starting point can be defined without asking the
tracker anything: `merged` starts the child from the target branch once
every one of the parent's PRs has merged into it, and `stacked` starts the
child from the parent's own branch in each repo the two share. Both are
computable from the store and git alone. The repos a ticket touches are
never declared on the dependency itself - they are derived from the
ticket's own slices' workspaces, because a workspace list already exists
as the ticket's own truth (`slices.yaml`) and a second, hand-maintained
list of "which repos this ticket touches" on the dependency would be
exactly the kind of fact that drifts the moment a slice's workspace
changes and nobody remembers to update the blocker that named it too.

A stacked child may start once the parent's latest gate round is clean,
and on nothing else: not "PR open," not "approved," not any other state a
tracker happens to expose. A tracker is a projection with its own review
workflow bolted on by whatever host it is - a PR can be open for reasons
that have nothing to do with the code being ready (an unrelated CI flake,
a human who hasn't gotten to it), and "approved" is a human's judgment
about the diff, not a claim that the diff will still apply cleanly as a
base for another branch. `jig gate`'s clean verdict is the one signal that
already means "the oracles pass and the reviewer found nothing outstanding
on this exact commit," which is the actual precondition a stacked child
needs from its parent. Enforcing this is out of scope for this ticket -
today nothing stops a stacked child from starting early - but the
dependency's own kind is defined now so that the later enforcement has
one clear rule to implement rather than inventing one against whatever
trackers happen to expose by then.

A chart hands over to tickets through exactly one door: `tickets.yaml` and
`jig graduate`. Graduate creates only the entries that have no id yet, in
file order, and never touches an entry that already has one - so running
it again after a chart's fog clears, or after a graduate call failed
partway through, creates exactly the tickets that are still missing and
leaves everything already minted alone. This is the same idempotent-replay
shape jig already uses for a frontier of slices: recomputing "what's left
to do" from what's on disk, rather than tracking a separate cursor of how
far a previous run got.
