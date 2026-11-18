package spend

// FromTranscript sums the usage of the Claude Code transcript at path, for
// session, subagent transcripts included: FromSession with no agent of its own.
func FromTranscript(path, session string, prices Prices) (Totals, error) {
	return FromSession(path, session, nil, prices)
}
