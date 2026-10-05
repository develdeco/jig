# Tests at agreed seams: the reviewer's contract

Briefs that list test cases one by one and demand that every new test fail when its rule is removed create large test suites and make every later session that reads them costly. The gate reviewer holds a change to its brief, so every unpinned rule becomes a finding, then a fix slice, then more tests.

Instead, briefs name only their seams - the public interfaces and critical paths - and the reviewer judges testing only at those seams. A missing test is a problem only at a seam the intent names, or as the proof of a defect the reviewer reports. A test elsewhere is at most a note.

This ADR names the seams in the intake skill and adds the principle to the reviewer's prompt.

## Seams in the intake skill

`skills/intake/SKILL.md` asks every brief for a `## Seams` section. It names:
- **the public interfaces the change is tested at,** such as a CLI command and its output, an exported function, or a file or wire contract like `review.json` or `slices.yaml`;
- **the critical paths through them,** one line each, each a behavior rather than a test.

The section never lists test cases, never asks for a test per rule, and never asks that a test fail when its rule is removed. It applies to all three shapes:
- for a bug, the seam is where its REPRO oracle runs;
- for a refactor, the seams are its guardrails.

Every slice lists `Seams` in its `from_brief`, so its builder reads it.

The skill stays within lint limits: at most 100 lines and 6000 bytes.

## The reviewer's contract

`reviewPromptTemplate` (`internal/verifydeliver/review.go`) gains one principle sentence after the `action:` line:

> Tests belong at the seams the intent names: a missing test is a problem only at one of those seams or as the proof of a defect you report, and a test elsewhere is at most a note.

`TestRenderReviewPromptMatchesDesignGolden` and its golden text change to match. There is no mechanical filter in jig; the model judges what counts as a seam, and an intent that names no seams leaves only defect-proving tests in scope.

## Configuration

`jig validate` does not require the section; jig never matches brief headings by name. `review.json` gains nothing, since the reviewer already reads the whole intent.

Every slice lists `Seams` in its `from_brief` via `jig validate <ticket>`, the same path every other brief section uses. Builders receive it as input; the reviewer reads the full intent.

## Test seams

Test at these public seams and nowhere finer:
- **The rendered review prompt,** through `TestRenderReviewPromptMatchesDesignGolden`: the exact bytes the reviewer is handed name the seams principle.
- **The intake skill,** through `lint/skills_test.go`'s limits: the skill's size and frontmatter stay within the budgeted byte and line counts.
