# Go packages are internal

Every library package lives under `internal/`, so nothing outside this module can import it. The public surface is the `jig` CLI - its commands, flags, output, and exit codes - and the store schema, not any Go type or function signature. This complements ADR-0003 (the store schema is the API that keeps concerns independent) by closing off Go imports as a second, accidental API, which leaves the packages free to change shape.

What stays at the root does so for its own reason, not because it's exempt from that rule: `cmd/jig` is the binary; `e2e/` and `lint/` are test-only; `skills/` must sit next to the skill directories it embeds, since `go:embed` cannot reach outside its own directory tree. `skills/` is technically importable, but its only export is the embedded file system, not a supported API.
