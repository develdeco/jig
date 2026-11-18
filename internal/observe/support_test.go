package observe

import "fmt"

// Run parses payload with the adapter, handles the event and returns what the
// harness reads, in the adapter shape (nil for nothing): the whole of a hook
// call but the process around it. An event the adapter does not map is ignored.
// Like Handle it never panics.
func Run(d Deps, a Adapter, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("observe: %s: panic: %v", a.Name(), r)
		}
	}()
	ev, err := a.Parse(data)
	if err != nil {
		return nil, err
	}
	return RunEvent(d, a, ev)
}
