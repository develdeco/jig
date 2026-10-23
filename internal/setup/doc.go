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
//   - harness.go: Harness is the table of the six harnesses (Claude Code, Codex,
//     the Gemini CLI, Cursor, GitHub Copilot, OpenCode): where each keeps its
//     hook file, and its Merge, Unmerge, Installed and Plan. codex.go and
//     gemini.go give the grouped shape claude.go has other event names and
//     entry fields; cursor.go and copilot.go give the flat shape in flat.go;
//     opencode.go writes the one JavaScript plugin OpenCode loads.
//   - project.go: the skills (Skill, InstallSkills, RemoveSkills, InspectSkills,
//     stamped so a file is known as jig's and by which version), the stub a
//     project gets in its untracked form, and the hint block in a memory file.
//   - doctor.go: Finding, the costs of each piece and GitConfig.InspectClone,
//     what `jig doctor` reads.
//
// The ownership of everything here is by detection: a command that matches
// OwnedCommand, a skill that carries the stamp, a block between jig's markers, a
// plugin whose first line is jig's. Nothing records what was written.
package setup
