# jig depend writes a ticket's dependency (GRAPH-23)

The brief left no question open; every call below was defaulted, so this file records
what was ambiguous, what was chosen and why.

- Kind default: `--kind` defaults to `merged`. It is the edge that cannot change where
  a branch starts, so it is the less surprising one to write by mistake; the stacked
  edge is asked for by name.
  Rejected: requiring `--kind` (a flag every call must spell for the common case).
- Refusals: a missing `--on`, a bad kind, `--clear` with `--kind`, a self edge, an
  unknown ticket (also a ticket of another store), a closed child (`TICKET_CLOSED`) and
  a cycle are refused before any write. A closed parent is allowed, since readiness
  already judges what each closure releases.
  Rejected: a second cycle check beside `validate`'s (one function, `ticketDepsCycle`,
  now takes the prospective edge and decides for both callers).
- One edge per parent: the same parent with the same kind prints `unchanged` and
  commits nothing; the other kind replaces the entry in place; a different parent adds
  an edge.
  Rejected: two edges to one parent with two kinds (readiness would have to rank them).
- Clear forms: `--on <P> --clear` removes that edge and `--clear` alone removes all,
  spelled as `jig rank --clear` is; clearing what is not there is `unchanged`.
- An existing branch is advice: a `stacked` edge on a ticket whose branch exists from
  main touches no branch, the store or a clone. `stack.Read` plans the ticket on the
  parent's branch, `jig next` says `behind` with the `git merge`, and the output of
  `jig depend` says so in one help line. The line shows for any new stacked edge and
  words itself conditionally ("if the ticket already has a branch"), since a ticket's
  default branch name exists before the branch does and the verb reads no clone.
  Rejected: refusing the edge (it strands tickets already begun) and rewriting the
  branch (jig never does).
- No mirror change: the edge appears at the next sync as `**Waits for:**` and the
  native link; `jig depend` does not call the host. A mirror test pins the same line
  as a graduate-written edge.
- Output: one `depend` block (`ticket`, `blocked_by` as `P:kind` joined by commas or
  `none`, `result` of `recorded`, `changed`, `cleared` or `unchanged`), with the help
  line "Run `jig next <T>` to see what it waits for".
