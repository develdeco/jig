# A skill is a short core with reference files

Every session skill sat at or near the 100-line, 6000-byte cap. A new rule then cost an old one, and trimming a skill to fit dropped rules twice in one run, each time a review round to put back. The cap guards something real, so raising it was not the answer; the detail most tickets never reach was.

## Decision

**A skill is a core plus reference files.** `skills/<name>/SKILL.md` is the core: what every ticket of its phase reads whole. `skills/<name>/references/<ref>.md` holds the detail only some tickets reach, with no frontmatter and a name matching `[a-z][a-z0-9-]*`. Both are embedded in the binary.

**The cap stays on every file.** The core and each reference are at most 100 lines and 6000 bytes; a reference that outgrows it splits. The core is paid on every load of the skill, so its size is a cost per ticket. 100 lines keeps a `head -100` preview whole, and a harness may truncate a long instruction file without saying so, so a rule past the cut is a rule that silently does not exist. A cap that applied to the core alone would let the detail grow back into one oversized reference.

**References are one level deep.** A core links its references, each with one line saying when to read it (``Read `jig skill <name> <ref>` when ...``). A reference links no other reference, because agents preview nested files partly: a chain of links is read to its first hop and not further. A reference may still name `jig skill <other>` for a whole skill. `lint` checks the caps, that every reference is linked from its core, that every link names a reference that exists, and that no reference links another, with table tests over fixture trees and the real tree.

**A reference is read with `jig skill <name> <ref>`.** Every harness that runs jig can run it, whereas a path beside the installed `SKILL.md` depends on where a harness installs skills and whether it reads files beside one. The command prints the embedded text and nothing around it, an unknown reference is a `VALIDATION_ERROR` naming the skill's references, and `jig skill` lists the references of each skill. An untracked project's stub still holds no copy of a skill or reference; it sends the agent to `jig skill <name> [<ref>]`, so the text a session reads is always the running binary's.

**The installers lay references out beside the core.** `jig skills`, `jig setup` and `jig attach` write each reference at `<root>/<name>/references/<ref>.md` with the same last-line stamp as `SKILL.md` where the skill is stamped. An uninstall removes the references that carry the stamp or equal the embedded text, then the emptied directories; a reference a person added stays, and so does its directory. `jig doctor` reads a skill whose reference is missing or differs as `stale`, so the install of an older jig, `SKILL.md` only, is reported and a reinstall fixes it.

## Comparison

- **Agent Skills guidance** keeps a skill body short (about 500 lines) and puts the rest in reference files read on demand. This is the same shape with a tighter body, because jig's skills are read on every ticket.
- **Cursor** advises rules under about 500 lines each; **Windsurf** limits a rule file to 6,000 characters and cuts what is past it without a warning; **Codex** reads a combined 32 KiB of project instructions; **Copilot** code review read only the first 4,000 characters of an instructions file until June 2026. jig's skills reach all of them. The 6000-byte cap matches Windsurf's silent cut-off, and Copilot's smaller limit was for one kind of file; the cap keeps a skill whole in the harnesses that cut without saying so.

## Rejected alternatives

- **Raise the cap.** The budgets above are limits of the harnesses, not of the skills; a longer core would be cut in some of them without notice.
- **Trim to fit, as before.** It dropped rules; the budget moved to the reader of a rule instead of the writer of one.
- **Reference paths beside `SKILL.md`.** They depend on a harness installing and reading sibling files, and an untracked project holds no copy at all.
- **References that link further references.** A nested file is previewed partly, and a rule behind two hops is not reliably read.
- **A cap on the core only.** The detail would move into one reference that grows without a bound.

## Consequences

- `skills/embed.go` embeds `*/SKILL.md` and `*/references/*.md`; `setup.Skill` carries its references.
- A moved rule is reached through its core's pointer line and nowhere else; a test that reads a moved sentence reads it from the reference.
- A skill that sat at its cap has room for new lines again.
