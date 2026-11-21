package journal

// The provenance of a landed line: who made the landing known. A session that
// ran `jig land` at the person's word is ProvenanceAgent, a person at a
// terminal ProvenancePerson, and a merge made on the host and found there is
// ProvenanceHost.
const (
	ProvenanceAgent  = "agent"
	ProvenancePerson = "person"
	ProvenanceHost   = "host"
)
