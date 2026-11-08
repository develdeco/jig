# A clone behind its origin is named by jig doctor and jig here (RUN-15)

Ambiguous: how a session learns that the machine map's clone of its project is stale, without `jig here` paying for a fetch.

Chosen: `internal/clonebehind.Check(dir, target)` counts `HEAD..refs/remotes/origin/<target>` and the reverse through gitx, with no fetch and no `git status`, so the answer is as of the clone's last fetch and says so. Only a clone with the target checked out is judged. Behind names `git -C <dir> pull --ff-only`; diverged gives both counts and no command; ahead, another branch and a missing clone add nothing. `jig doctor` also notes a clone with the target checked out but no `origin/<target>`. jig advises and never pulls (ADR 0034).
