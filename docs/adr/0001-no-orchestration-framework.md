# No orchestration framework

Jig's dispatch loop needed checkpoints, resumability, and a way to route review findings back into work, which a graph/workflow orchestration framework (checkpoints, interrupts, conditional edges) also provides. We chose slice state files on disk plus a frontier while-loop instead: resume is just recomputing the frontier from what's on disk, and since review has no back-edges, there is no cycle to wire in the first place. The framework's machinery would buy nothing a plain loop over files doesn't already give us.
