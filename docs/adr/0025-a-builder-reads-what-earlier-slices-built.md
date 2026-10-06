# A builder reads what earlier slices built

Every build session starts cold. BS-3's builder spent its first ten minutes learning the code it was about to change (36 file reads, 18 greps and dozens of shell reads), and on a multi-slice ticket each later slice pays that again for code an earlier slice of the same ticket just wrote. Matt Pocock's implement flow shares exploration notes between steps, and kun prefers reusing what is known to a cold restart.

## The rule

- **slice.json gains `earlier_slices`:** every slice of the ticket whose green jig verified before this dispatch, in the order they first verified, each with its latest verified attempt:
  - `id`;
  - `summary`, from that attempt's `result.json`;
  - `files`, the files that attempt changed: the lease diff from the head at its dispatch to its verified commit. They stay empty when that range is not in this lease, as for a slice of another repo.
- **The dispatch line records its base,** the lease head at dispatch (`commit`), which is what the files are measured from.
- **The dispatch prompt names the field** in its line about slice.json.

Builders in one repo run one at a time, so an attempt's range holds only its own commits.

Sizing slices so one cohesive change is one slice, the other half of build-speed item 6d, is method, not mechanism: it belongs in the intake skill.

## Test seams

- `frontier.Run` through the fake backend, through `TestRunHandsALaterSliceWhatEarlierSlicesBuilt`: slice b, which waits for slice a, reads a's summary and the alpha/ files a changed, and never itself.
- The dispatch prompt, through its goldens.
