When `jig next` and `jig gate` say this session is not fresh, or unknown:

Spawn the subagent they advise (the Agent tool, `subagent_type: general-purpose`, `model: opus`, no worktree) with their prompt. Wait for it as `jig gate` says and relay its verdict line; as a subagent, whose turn's end is its hand-back, you hand back with the round open instead. Never schedule a wakeup or stall with another tool. Never run `jig review context` yourself: jig sees you and your subagents as one session, so it could not tell who closed the round.
