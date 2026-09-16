# One binary plus skills

Jig splits work between a Go binary and session skills rather than encoding everything as CLI logic. Rules-decidable control flow (dispatch, oracles, journaling) lives in the binary; judgment calls (drafting a brief, classifying a finding) live in session skills run against that binary. A second binary is only earned by a second consumer, operator, or cadence - adversarial isolation between sessions comes from what a session sees and which model runs it, never from which executable spawned it.
