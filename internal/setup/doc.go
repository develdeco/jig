// Package setup installs and removes the artifacts jig places around a
// person's tools: harness hook entries, the global git hook wrappers, and the
// skills, each per explicit scope and harness, idempotently, touching only
// what jig wrote, and it reports what is missing and the command that adds it.
// It backs `jig setup`, `attach`, `hooks`, `githook` and `doctor`.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package setup
