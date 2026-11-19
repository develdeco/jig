package setup

// ClaudeCommand is the command of every Claude Code entry: jig is the program
// part JigCommand gives, and the event arrives on stdin.
func ClaudeCommand(jig string) string {
	return ObserveCommand(jig, "claude")
}

// PlanClaude works out installing jig's Claude Code entries into the settings
// file at path, or taking them out when uninstall is set.
func PlanClaude(path, jig string, uninstall bool) (FilePlan, error) {
	return planFile(path, jig, uninstall, MergeClaude, UnmergeClaude)
}

// Unmerge removes jig's entries from the document in data. A result of nil with
// a change means the file was jig's alone and goes.
func (h Harness) Unmerge(data []byte) (out []byte, changed bool, err error) {
	return h.unmerge(data)
}
