# Demos are recordings of the build's end-to-end scenarios

ADR 0014 made the demo a model session of its own, run after a clean reviewer round, that decides what to show and makes the media. jig's own flows are shown by VHS tapes that CI renders. Both are a second script beside the tests: a tape breaks whenever a prompt changes (#78 needed a hand fix), and the demo session re-does by hand what the end-to-end tests already drive. The owner settled a different source on 2026-10-05: the demo is a by-product of verification, recorded while the build's end-to-end scenarios run.

## Decision

- **Scenarios.** Builders derive named end-user scenarios from the brief's critical paths and write them as end-to-end tests (ADR 0027's chain per critical path). For jig's own repo, its end-to-end tests are the scenarios.
- **When recording happens.** In the build phase, while those tests run: every build that runs a scenario records it, so the fix builds that follow findings record again. Each recording carries the commit it ran at. The gate records nothing; it stays read-only.
- **A failing scenario** is a testing failure: the slice is not green and the build loop handles it, never a review finding. The reviewer reads the recordings as evidence for a more detailed review.
- **Where recordings live.** Locally only, under the jig home, as ADR 0014's media already do; the store holds a manifest. CI records nothing, so the VHS tapes and `demo.yml` go.
- **Recorders.** jig records as much as it can by itself, starting with a terminal recorder: a capture of what a program wrote to its terminal, rendered as an animated SVG a pull request shows inline. For web projects, the project's own end-to-end harness (for example Playwright) records and jig collects what it wrote; jig has no browser. Other kinds of recording are the project's to describe in its CLAUDE.md.
- **Capture.** A recorder captures through a pseudo-terminal, so a program that draws differently on a terminal is recorded as a person sees it. jig's own end-to-end tests are the exception (owner, 2026-10-06): they record by stamping each write to the pipes they already read. jig's output does not depend on a terminal (only its stdin is checked, for interactive triage), and a pseudo-terminal would merge stdout and stderr, which those tests assert on separately. Both captures make the same recording.
- **Which recordings reach the pull request.** Builders tag each recording with its scenario, its commit, and an optional flow and step. At publish, a short model session picks the recordings that show the change and composes flows in step order: lone scenarios stay separate, connected ones go together, and mixes are allowed. jig validates the pick against the manifest and attaches the files, through the attach path ADR 0014 built.
- **ADR 0014's demo session is retired** once publish picks from recordings.

## The recording format

`internal/termrec` holds the terminal side. A `Cast` is asciicast v2's model: the terminal's size and the output written to it, each write stamped with its time (`ReadAsciicast`, `WriteAsciicast`), so asciinema 2's files read as one, and asciinema 3's when written as v2. A rune a write split is carried into the next event, since an asciicast event cannot hold part of one.

`Cast.SVG` plays the writes on a small terminal model and renders one frame per visible change, stacked and stepped through by a CSS animation, which a browser plays inside an `<img>`. The model covers text, line control, colors, erasing, cursor moves and a progress line redrawn in place; it reads past mode switches, control strings and the alternate screen, so a full-screen program's last screen stays in the last frame after it exits. Each distinct row is drawn once and placed in every frame that shows it, and each run of text is pinned to its cells (`textLength`, a wide rune at its own cell), so columns line up whatever monospace font the viewer has. Pauses are capped (2 s by default), the writes in one 50 ms slot make one frame, a long recording gets wider slots so it keeps at most 600 frames, and a rendering over 8 MiB gets wider slots again until it fits, under the 10 MB GitHub takes for an attached image. `Cast.FinalText` is the last frame as text, the terminal model's test seam.

## Built in steps

Each step is its own pull request, and until the last ones land ADR 0014's demo session and the tapes stand:
1. The recording format and its SVG rendering (`internal/termrec`).
2. Capture: a pseudo-terminal recorder, and the pipe-stamping one jig's own tests use.
3. Build-phase collection: jig's oracle run at a builder's green gives the scenarios a directory to record into, verifies what they wrote, and records the manifest with the commit.
4. jig's own end-to-end tests record.
5. Publish picks the recordings and composes the flows; the reviewer prompt names them as evidence.
6. The gate demo session is retired.
7. The tapes, `demo/fixture` and `demo.yml` are retired.
