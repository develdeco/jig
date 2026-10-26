# jig advises context and runs no context tool

[ADR 0026](0026-a-code-graph-gives-the-builder-its-starting-points.md) gave a builder starting points: before each dispatch jig brought the lease's code graph up to date and asked it about the slice's goal. [ADR 0032](0032-jig-attaches-to-the-harness.md) moved that run into `jig context`. Today, when a store's config names `graphify` under `context:` and the binary is on PATH, every `jig context` runs `graphify update .` (bounded at 5 minutes) and `graphify query` (1 minute) in the person's clone, adds `/graphify-out/` to the clone's `info/exclude`, prints a `related` block and records a `graph` line in the machine ledger.

Under ADR 0032 that is a box. jig runs, on the session's behalf, a tool the session already has or can choose (a graph skill, search, an editor's symbols), in the person's clone, with jig's question and jig's bounds, and holds `jig context` for as long as the tool takes. Whether a graph is worth building, what to ask it and how to read the answer is the session's judgment. The owner's rule (2026-10-26, digester chart decision 45): jig does not run context tools for the agent; it says what context to gather, and the agent gathers it with the tools at its disposal. The rule covers context tools only. git, gh, the claude CLI, the oracles and jig itself are jig's own work (the store, verification, publish), not context for the agent, and they stay.

## Decision

**jig gives the records it holds and advises the rest.** `jig context` returns what only jig knows: the brief sections the slice is cut from and the Seams, the earlier slices with their commits and files, the oracle and how long jig's last run of it took, the failed attempts, the questions and the notes. The code itself is the session's to find. jig runs no program to find it, so `jig context` costs a read of the store and of the clone's history, nothing more.

**The advice is one line in jig's words, naming no tool.** `jig context`'s help says that the code the goal touches is the session's to find with its own tools (search, a code graph, the editor's symbols) and, when slices have verified, to start from the files they changed, which the `earlier_slices` block lists. The build skill says the same: read the context, then find the code with your own tools. The JSON form carries no help, as before; the skill carries the rule for every caller.

**A project names its tools where its harness reads them.** A project that wants its sessions to use a particular context tool says so in its own instruction file (`AGENTS.md`, `CLAUDE.md`), which the harness already loads. jig carries no tool names and no switch for them.

**What goes with the run:**

- `internal/graphify` (the plane, `Detect`, `DetectWith`, `Noop` and the unused `Affected`), and in `slicectx` the graph query, `Input.Graph`, `Input.RecordGraph`, `Context.Related` and the `related` block. The reviewer's `must_review` and publish's oracle scoping, which ADR 0026 named as later uses of the plane, are not built: they would be the same box.
- The `context:` key. Nothing reads it, and the config loader stops decoding it. A store that has it still loads, since the loader ignores keys it does not know, and the v2-to-v3 migration edits the document, so the key travels along untouched.
- The machine ledger's `graph` line: `ledger.Graph` and `journal.EventGraph`. Lines already in a ledger stay, and `jig events` shows them as it shows any line no fold uses.
- The `/graphify-out/` exclude. jig writes none. Lines it wrote into clones' `info/exclude` stay: jig cannot tell its line from one the person wrote, and an exclude line is harmless. Keeping a tool's output out of commits is the tool's or the person's job.
- `JIG_LIVE_GRAPHIFY` and `TestGraphifyLive`, with the package. (`TestRunWithTheRealGraphify` went with the frontier.)
- README's Safety list: on the person's machine jig runs git, gh, the repo's oracle commands and itself.

ADR 0026 is superseded: its premise holds (a session starts cold, and starting points help), and the session meets it with its own tools. ADR 0032 is amended: `jig context` returns no code-graph starting points, and its verdict row for 0026 reads retire.

## Consequences

- `jig context` is a read. It never waits on a cold graph, writes nothing into the person's clone, and prints the same whether or not a context tool is installed.
- A session with a graph skill gets what `related` gave by asking it, with its own question. A session without one searches, as it did when the project had not opted in.
- The ledger no longer records which starting points a session was handed. Nothing read that record.
- A project that had opted in sees the `related` block disappear with no warning. The key is inert rather than refused, so no store breaks.

## Rejected alternatives

- **Run the graph only on request (`jig context --graph`).** Still jig running the session's tool, with jig's question and bounds; a box the session opts into is still a box.
- **Print the command for the session to run.** It names one tool in jig's output and hand-holds a session whose harness already knows which tools it has.
- **An advice string under `context:` that `jig context` prints.** A second place for what the project's instruction file already says, seen only by sessions that call `jig context`.
- **Keep `related` as a field that is always empty.** Nothing would fill it, and a field that cannot fill is noise in the output.

## Test seams

- `cmd/jig`'s `jig context`, with a store whose config still names `graphify` under `context:` and an env whose `lookPath` records every lookup: the command looks up no program, prints no `related` block and no `related` key in `--json`, and its help carries the advice, pointing at the earlier slices' files only when there are some.
- `slicectx`'s tests, less the graph ones.
- `lint`: `TestArchitectureDoc` (the module table loses `internal/graphify`) and `TestSkillsBudget` (the build skill stays within its 6000 bytes).
