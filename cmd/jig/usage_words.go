package main

// notMeasured is what a figure of an oracle run that could not be read is
// shown as: not measured is not zero.
const notMeasured = "not measured"

// orNotMeasured is s, or notMeasured when s is empty.
func orNotMeasured(s string) string {
	if s == "" {
		return notMeasured
	}
	return s
}
