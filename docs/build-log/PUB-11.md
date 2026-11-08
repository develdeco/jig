# Picks offer only the ticket's own recordings; the round line says what a round asked (PUB-11)

Ambiguous: which recordings a publish offers, and what the body's Gate rounds line says.

Chosen:
- A recording is the ticket's own when its tag's `file` (journaled as `source`) is a file the ticket's commits added, modified or type-changed, from the merge base of the reviewed head and `origin/<publish base>` up to the head (`ownSources`, per repo, in that repo's lease directory). The grain is the test file. A recording with no source (an old journal line) or of an untouched file is left out silently, not listed under `picks_dropped`. With none left, publish prints no `picks:` row and no warning.
- Renames are off in the diff, so a renamed test file counts under its new path; a deleted one is not offered.
- The Gate rounds line prints what the round asked: `n fix slices asked`, `asks for the person`, `notes only`; clean, not-independent and the scope parenthesis are unchanged. A carried-over round (GATE-16) prints `carried over`. Verdict constants and `report.yaml` do not change.
- The publish skill only had a same-length rewording (5983 of 6000 bytes).

Provenance: the brief's calls, as approved; the carried-over wording follows GATE-16 now that main holds it.
