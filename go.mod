module github.com/develdeco/jig

go 1.27

require (
	golang.org/x/sys v0.48.0
	gopkg.in/yaml.v3 v3.0.1
)

retract v0.1.1 // headless sessions never start: the Claude Code CLI refuses jig's argv
