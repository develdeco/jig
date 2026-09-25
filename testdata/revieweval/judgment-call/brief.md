# Brief: retry helper

Add a `netutil` package with a `Retry` function that calls a request
function up to a fixed number of times, stopping at the first success. The
brief leaves open whether a request that may already have taken effect
before failing (a non-idempotent request) is safe to retry automatically.
