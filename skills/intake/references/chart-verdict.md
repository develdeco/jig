Make a chart with `jig chart new <name> --title "<t>"`, not by hand. Decision tickets: one per grilling session, entered as they resolve, each with a `key:` (its store key; omit if the store declares one). `jig validate <name>` lists every problem and mints nothing; `jig graduate <name>` creates the entries without ids and writes them back, a re-run makes only new ones.

A batch spanning areas takes a chart verdict, ordering its tickets.

A chart verdict ends a ticket run: report what graduated and what `jig next <chart>` says, then read the prompt's intent from its words (examples, no phrase list); unsure, ask.

- **No intent** (nothing past the brief: "fix this: ...", "fix this, go with your recommendations"): name the prompts that launch one ticket ("Take <ticket> to PR"), several ("Take A and B to PR") or the chart ("Take <chart> to PR"), stop, ask nothing.
- **Intent, not fully explicit** ("take it to PR", "fix this and open a PR", "take it from brief to PR, go with your recommendations"): ask once to confirm the launch it implied (the chart, or the tickets carrying a named ticket's work), "not now" the other answer; the run goes with your recommendations only if the yes says so; unattended, stop. Why: a ticket's terms ask for a ticket run, which a chart verdict ends.
- **Fully explicit** (recommendations and working to the end, whatever the shape: "go with your recommendations, keep going until it is all done", "fix it end to end with your recommendations, I'll be away"): say why in one line, run `jig skill chart`, ask nothing.

A named ticket whose grilling ends in a chart enters `tickets.yaml` with its own `id`, `title` and `body` scoped to its part; graduate keeps it `existing`.
