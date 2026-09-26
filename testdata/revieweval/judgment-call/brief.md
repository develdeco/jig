# Brief: retry helper

Add a `netutil` package with a `Retry` function that calls a request
function up to a fixed number of times, stopping at the first success.
Callers use `Retry` both for read requests and for calls that create a
record.
