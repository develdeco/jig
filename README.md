# gh-pages

The GitHub Pages site for jig, served at https://develdeco.github.io/jig/.
It holds no code: `main` is the project, this branch is what the site serves.

## diagrams/

Interactive diagrams drawn with [archify](https://github.com/tt-a1i/archify) 3.2.0.
Each diagram is a pair:

- `<name>.json` - the archify candidate, the input. Edit this.
- `<name>.html` - the page archify renders from it. Never edit by hand.

The Mermaid versions in `docs/diagrams.md` on `main` are the ones kept in
step with the code; these pages are redrawn when they drift. To redraw one,
set `meta.repository.revision` in its candidate to the commit it was drawn
from, then from this branch's root:

```bash
node <archify>/bin/archify.mjs finalize <type> diagrams/<name>.json diagrams/<name>.html --repo-root <jig checkout at that commit> --quality showcase
```

| Name | archify type |
|---|---|
| `system-context` | `architecture` |
| `domain-model` | `class` |
| `ticket-flow` | `workflow` |
| `commit-verification` | `sequence` |
| `slice-states` | `lifecycle` |

Commit the candidate and the page only. archify's receipts
(`*.delivery.json`, `*.finalize*.json`, browser evidence) record local paths
and stay out of the branch.
