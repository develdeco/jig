# The roster costs tickets only

The screens were unusable on a store of a few hundred tickets: the list took 5 s to read, the first frame showed the stream's replay in the order it arrived and each row said "reading...", and a key took 1.7 s. A profile of the read and of the frames put the time in three places, and each was a cost that grew with something other than the number of tickets.

## Measured, on a store of 247 tickets and 28 charts (Windows, a clone whose config the in-process reads decline)

- `jig next --json` took about 5 s. The digest of each ticket cost 3.5 s of it: git questions 1.6 s (76 processes in all, one for each branch the stack plan and the merge facts looked at), merge facts 0.7 s, the chart index decoded again for each ticket 0.5 s, gate findings 0.4 s. The overlaps of branches, which no screen reads, cost 0.7 s and 15 processes. About 19 ms for each ticket.
- A frame cost 1.7 s with the list complete: the work clock of every ticket was computed for each row, and it walks every line of every journal the replay held. A key, a tick and each batch of the stream cost one such frame.
- The runner was one lock: a ticket's status waited behind a roster read of seconds, and each store commit re-read the whole roster.

## Decision

**The roster is a verb of its own.** `jig next --json --roster` prints each ticket's row as `jig next --json` prints it (one function makes both) and the charts, and nothing a screen does not read. The screens read it and nothing else of the list.

**What a read costs is the tickets, by a small constant for each.** What is asked of git is asked once for each repo: one listing of its branches, and a tip, the target's tip and a stack's tips are lookups in it. An answer about commit ids never changes, so it is kept on this machine under the jig home, by the ids, and asked once: whether one commit is in another's history, which files a branch changes past a base, whether two branches merge. What every ticket shares is read once for the run and looked up. The tickets' own reads run concurrently, bounded by the processors, with the rows kept in id order. The host is never asked in the middle of the list.

**No digest is kept between reads.** A digest kept in a file, or from the last run, could say what the store no longer says, and the screens exist to tell the truth. Only what cannot change is kept. If a measured figure misses its bar, the cost is named and the next change reads less; it does not hide behind a cache of derived words.

**A frame costs what it draws.** A row of the project's list or of a chart shows no time, so it computes no clock; the clocks are computed where a screen shows one. What a screen reads of a ticket's journal stands while the ticket's lines, status and digest are as they were. Main draws nothing but "reading the store..." until the roster lands, and then its rows in the roster's order.

**The commands wait by class.** A key's command, the read of the screen the person opened, and the reads that keep the rest warm: the next is the earliest of the highest class, a running command is not interrupted, a key's command is never dropped, and a background read asked for while the same one waits adds nothing, so a burst of commits costs one more roster read.

## Consequences

- A read of a store of 1,000 tickets and 500 branches starts the same few git processes as one of 12, and the same one more once the ids it names have been asked about.
- The git processes of a roster read are one for each repo whether git's own reads in process work on the clone or decline it. Widening them to a clone with a worktree config would remove that one too, and is gitx's.
- A partial read of the tickets a store commit changed is not built: it would be a second derivation that has to equal the full one for every relation a row reads.
- A kept fact is wrong only if a commit id names two histories; the file is a cache and is safe to delete.
