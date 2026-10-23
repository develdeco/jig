// Package setup installs and removes the artifacts jig places around a
// person's tools: harness hook entries, the global git hook wrappers, and the
// skills, each per explicit scope and harness, idempotently, touching only
// what jig wrote, and it reports what is missing and the command that adds it.
// It backs `jig setup`, `attach`, `hooks`, `githook` and `doctor`.
//
// What is here:
//
//   - ordered.go: JSON read into an ordered form and written back in the file's
//     own layout, so merging into a person's settings file does not reorder it.
//   - command.go: the hook command contract: JigCommand builds the program part
//     of every command jig installs, OwnedCommand recognizes one.
//   - claude.go: MergeClaude and UnmergeClaude put jig's Claude Code hook
//     entries into a settings document and take them out; PlanClaude works a
//     change to a file out before it is made.
//   - githook.go: the global git hook wrappers (GitConfig.InstallGlobal, which
//     chains each repository's own hooks and the core.hooksPath that was set
//     before) and the block for a repository that sets core.hooksPath for itself
//     (GitConfig.InstallClone).
//   - exclude.go: ExcludeFile keeps a file jig made out of a repository's git
//     status through its .git/info/exclude.
//
// The harnesses other than Claude Code, the skills, the hint, the project forms
// and the doctor report are the next ticket's files here.
package setup
