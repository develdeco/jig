// Package observe turns what an agent harness reports (session start, a
// prompt, a tool call, a stop, session end) and git's post-commit into neutral
// Events, and acts on them: session records in the machine ledger, background
// verifications of new commits, and the context and notes handed back to the
// harness. A per-harness adapter maps that harness's payload onto Event; the
// core never reads a harness's own shape. It backs `jig observe`.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package observe
