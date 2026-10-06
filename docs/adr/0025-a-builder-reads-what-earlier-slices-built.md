# A builder reads what earlier slices built

Every build session starts cold. BS-3's builder spent its first ten minutes learning the code it was about to change (36 file reads, 18 greps and dozens of shell reads), and on a multi-slice ticket each later slice pays that again for code an earlier slice of the same ticket just wrote. Matt Pocock's implement flow shares exploration notes between steps, and kun prefers reusing what is known to a cold restart.

## The rule

- **slice.json gains `earlier_slices`:** every slice of the ticket whose green jig verified before this dispatch, in the order they first verified:
  - `id`;
  - `summary`, from its latest verified attempt's `result.json`. A fix turn after a red oracle run or a dirty tree asks for a summary of the slice's whole change, not just the fix;
  - `files`, the files the slice's attempts changed: the union, over its attempts, of each attempt's own range, from the lease head at its dispatch to its verified commit or, for an attempt that ended otherwise (a question, a failure), the lease head when its last turn ended. At most 50, with `files_omitted` counting the rest.
- **The journal records each range's ends:** the dispatch line's `commit` is the lease head at dispatch, and a result line's `head` the lease head when that turn ended (`journal.VerifiedSlices`).
- **The dispatch prompt names the field** in its line about slice.json.

Builders in one repo run one at a time, but the lease is never reset between attempts, and other slices may run between two attempts of one slice (while it waits on a question, say). So a slice's own commits are only its attempts' ranges, never the span from its first dispatch to its verified commit. A range git cannot resolve (an older journal without dispatch bases, a commit no longer in the lease) adds no files.

Sizing slices so one cohesive change is one slice, the other half of build-speed item 6d, is method, not mechanism: it belongs in the intake skill.

## Test seams

- `journal.VerifiedSlices`, through `TestVerifiedSlices`: two attempts of one slice with another slice verifying in between get two ranges that leave the other slice's commits out.
- `frontier.Run` through the fake backend, through `TestRunHandsALaterSliceWhatEarlierSlicesBuilt`: slice b, which waits for slice a, reads a's summary and the alpha/ files a changed, never itself, and no earlier slice of workspace beta lists an alpha/ file.
- The dispatch prompt, through its goldens.
