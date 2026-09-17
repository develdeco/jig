# Disk-only pipeline seam

`frontier` and `verifydeliver` are two phases of one pipeline but needed a seam that wouldn't force them into one process. They share zero in-memory state; the store on disk is their only interface. That seam is deliberately a `git mv` away from a split into two binaries, should a second consumer ever justify one.
