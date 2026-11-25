A test diet is a refactor: its PR names each removed test's seam test and `go test -cover` before/after.

A bug has no REPRO slice: jig verifies a slice only when its oracle passes, so one red before any fix could never go green. The fix slice's first commit adds the repro test, red on the base; the fix commit turns it green, and the slice's oracle runs that test.
