# RR-3 - retrieval notes

## seams

- goal: skills/intake/SKILL.md asks every brief for a ## Seams section (public interfaces and critical paths, never test cases or a test per rule, for all three shapes) and every slice lists Seams in from_brief, within the skill lint limits, as the brief's "The intake skill" specifies. reviewPromptTemplate gains the one principle sentence after the action: line, quoted in "The review contract", and TestRenderReviewPromptMatchesDesignGolden's golden changes to match; no filter in jig, no new review.json field, no validate check. Write the short ADR and the two-line DECISIONS.md entry, as "Docs" specifies. Leave go test ./... green.
- commit: 0b508b248f95b803cbe9c205b4d32ba6e2bcc630

## seams-from-brief

- goal: skills/intake/SKILL.md does not yet say what "The intake skill" requires last: every slice lists Seams in its from_brief, so its builder reads the seams (builders read the brief only through the sections from_brief lists). Add that to the skill's Seams or Brief output section, within the lint limits. Also fix ADR 0016's line 41, which overclaims that lint/skills_test.go checks for the Seams section: the lint checks only the skill's size and frontmatter.
- commit: f299ae737efb78113bdf138d14d4c436a6c4d641

