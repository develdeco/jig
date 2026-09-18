# Brief: concurrent job runner

Add a `worker` package with `RunAll(jobs []string, task func(string))` that
runs task(job) for every job in jobs, one goroutine per job, and waits for
all of them to finish before returning. Target Go 1.22+, where each range
iteration binds a fresh copy of the loop variable.
