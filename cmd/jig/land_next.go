package main

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/land"
)

// nextLanding is what the next landing is: a published ticket that waits for
// this one, else the container this one lands into once its tasks have all
// landed (land.Complete), else what the container still waits for, else that
// nothing waits.
func nextLanding(d land.Deps, ticket string) string {
	st := d.Store
	all, err := st.Tickets()
	if err != nil {
		return "Nothing waits on a landing here"
	}
	for _, id := range all {
		rec, err := st.ReadTicket(id)
		if err != nil || rec.Closed != nil {
			continue
		}
		for _, b := range rec.BlockedBy {
			if resolved, err := st.ResolveTicket(b.Ticket); err == nil && resolved == ticket {
				if got, _ := land.Landed(st, id, ""); len(got) == 0 && published(st, id) {
					return fmt.Sprintf("The next landing is `jig land %s`, which waited for it", id)
				}
			}
		}
	}
	if rec, err := st.ReadTicket(ticket); err == nil {
		if cs, err := st.ContainersOf(ticket, rec); err == nil && len(cs) == 1 && published(st, cs[0]) {
			done, waiting, err := land.Complete(d, cs[0])
			switch {
			case err != nil || done:
			case len(waiting) == 0:
				return fmt.Sprintf("Its tasks have all landed: the next landing is `jig land %s`", cs[0])
			default:
				return fmt.Sprintf("%s lands once %s have: the next landing is `jig land %s`", cs[0], strings.Join(waiting, ", "), waiting[0])
			}
		}
	}
	return "Nothing waits on a landing here"
}
