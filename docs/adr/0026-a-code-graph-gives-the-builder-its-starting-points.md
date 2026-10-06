# A code graph gives the builder its starting points

A build session starts by learning the code it is about to change: BS-3's builder spent its first ten minutes on 36 file reads, 18 greps and dozens of shell reads before its first edit. ADR 0025 hands a later slice what earlier slices of the ticket built; the first slice, and every slice's untouched code, still start cold. jig shipped a graphify plane in v0.1 (an interface, a `graphify affected` shell-out and a no-op fallback) with no caller.

graphify (PyPI `graphifyy`, Apache-2.0 and MIT) builds a knowledge graph of a repository locally from tree-sitter ASTs, with no LLM for code, and answers queries over it. On jig's own repo (about 400 files) on the Windows dev machine, `graphify update .` takes 25 to 36 s and a query about 4 s, and the query for 6h's goal put `ApplyRound`, `findings.go`, `review.go`, `gate.go` and revieweval's runner first.

## The rule

- **Opt-in and optional.** A project opts in with a `graphify` key under `context:` in `project.yaml`, and jig uses graphify only when the binary is on PATH (`graphify.Detect`). Otherwise slice.json's `related` is empty and nothing is journaled; nothing fails. jig's own CI needs no Python.
- **Before each dispatch, jig brings the lease's graph up to date and queries it.** It runs `graphify update .` in the lease (incremental, code only), then `graphify query` with the slice's goal and, as seeds, up to 20 files earlier slices of the ticket changed (ADR 0025). Both runs are bounded (5 minutes and 1 minute), with the process tree killed past the bound.
- **slice.json gains `related`:** the files the graph links to the goal, in the graph's order, most directly matched first, each with up to 8 symbols as `<label> L<line>`; at most 25 files. Nodes without a source file (packages, libraries) are left out. The dispatch prompt names the field.
- **The graph never touches the build.** jig adds `/graphify-out/` to the lease's own `info/exclude` before the first run, so a builder never commits it and jig's clean-tree checks (`cleanHead`, `trackedChanges`) never see it. A project that commits its own `graphify-out/` gets no such protection; it should not.
- **A failure costs only the context.** jig journals a `graph` line per dispatch, `pass` or `fail: <reason>` (graphify's stderr and the end of its stdout, where it prints the cause), with its seconds; a failed update or query leaves `related` empty and the attempt goes on.

The reviewer's `must_review` and publish's oracle scoping are later uses of the same plane.

On Windows, graphify's incremental update fails on its own cache files under a path near 260 characters; leases under the usual jig home are well below that.

## Test seams

- graphify's output parsing (`parseQuery`, on graphify 0.9.77's own lines) and `DetectWith`, which takes the binary lookup so no test edits PATH.
- `TestGraphifyLive` and `TestRunWithTheRealGraphify`, behind `JIG_LIVE_GRAPHIFY=1`: the real binary updates a small Go repo's graph and a query finds its function, and a real `frontier.Run` over the fixture repo hands slice a related files under the workspace its goal is about.
- `frontier.Run` with a fake plane, through `TestRunHandsTheBuilderTheCodeTheGraphLinksToItsGoal`: a graph that answers fills `related` by file in order, is updated before it is queried, is asked the goal, journals a pass, and leaves `graphify-out/` excluded in the lease; a graph that fails leaves `related` empty, journals the reason, and the slice still goes green; no graph means an empty `related` and no journal line.
- The dispatch prompt, through its goldens.
