package land

import "github.com/develdeco/jig/internal/store"

// Command is the `jig land` that lands ticket, as a session is told to run it.
// A task two issues share lands per issue, so it names the issue it lands into
// (--issue into), or <ID> when into is none of its issues.
func Command(st *store.Store, ticket, into string) string {
	cmd := "jig land " + ticket
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return cmd
	}
	cs, err := st.ContainersOf(ticket, rec)
	if err != nil || len(cs) < 2 {
		return cmd
	}
	for _, c := range cs {
		if c == into {
			return cmd + " --issue " + into
		}
	}
	return cmd + " --issue <ID>"
}
