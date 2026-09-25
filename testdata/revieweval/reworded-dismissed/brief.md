# Brief: queue helpers

Add a `queue` package with `Labels(tasks []string) string` that joins task
names with ", " for a one-line status display, and `Highest(tasks
map[string]int) string` that returns the task with the highest priority
value, or "" when tasks is empty.
