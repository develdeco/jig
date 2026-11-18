# The suite slot is removed and nothing replaces it

[ADR 0051](0051-heavy-runs-share-the-machine-by-measured-need-in-arrival-order.md) made a heavy oracle (`heavy:` in the manifest) ask one machine-wide queue for a share, measured from its project's earlier runs, and wait for it in arrival order. The reason was a machine on which two whole suites at once ran past their time bound. Two things changed since. [ADR 0052](0052-jig-witnesses-a-run-and-starts-none.md) moved the run into the builder's own shell, and [ADR 0053](0053-a-gate-round-and-publish-run-no-test.md) took the gate's and publish's runs away, so jig no longer decides when a run happens or how many happen at once: that is the agent's and its harness's. And in use the queue turned contention into waiting: about 34 agent-hours of slot wait while the machine averaged about 20 percent CPU under one suite.

## Decision

**The slot is removed and nothing replaces it.** `jig verify` starts its command at once, whatever else runs on the machine. There is no queue, no advice line and no command that reports load. Admitting runs to a machine is scheduling a resource jig does not own, and a harness that starts several builders is the one that knows how many the machine takes.

**What goes.** `internal/slot`, `internal/need` and `internal/machine`; the queue directory helper; the `slot-wait` and `slot-taken` journal events and the oracle line's `shared` field; the `waiting` section of `jig here` and `jig status` and its JSON field; the TUI's labels for the slot lines; the marker's `slots:` and the manifest's `heavy:`; and the measured-need record under `<jig home>/needs/`.

**What stays.** Each witnessed run's `oracle` and `final-oracle` line keeps the CPU time, peak memory and basis of its process tree (`cpu_ms`, `peak_bytes`, `peak_basis`), written under the rules it had, and `jig status`'s last runs and `jig here` show them in words. It costs nothing to keep, is the one honest account of what a run used, and is the first input of workload metrics, which are parked.

**Old files still load.** A manifest with `heavy:` and a marker with `slots:` of any value, one that was invalid included, load as if the key were absent: both parsers ignore keys they do not know. An older journal's `slot-wait` and `slot-taken` lines and `shared` field are read and shown as any event the reader has no special case for. A leftover `slot-queue/` or `needs/` directory under a jig home is not read or removed.

## Consequences

- A run never waits for another. Two whole suites can run at once on one machine and, if the machine is too small, run slow; whoever starts them owns that choice.
- A project's `heavy:` and `slots:` lines do nothing and can be deleted.
- Workload metrics, a future slot or any advice on machine load are separate work, not started here.

This supersedes ADR 0051.
