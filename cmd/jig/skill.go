package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/setup"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec:  cmdSpec{"skill", "jig skill <name>: print a skill's instructions as this jig carries them; with no name, list the skills", nil, false},
		Order: 521,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSkill(e, args, stdout)
		},
	})
}

// cmdSkill implements `jig skill [<name>]`. A skill's text is printed as it is
// embedded, with nothing around it, so an agent reads it as the skill and a
// stub in a project, which never copies a skill, always sends it to the text
// of the jig that runs.
func cmdSkill(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "skill")
	names, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	name, err := atMostOne(names, "skill", "skill name")
	if err != nil {
		return renderErr(stdout, err)
	}
	if name == "" {
		var rows [][]string
		for _, n := range embeddedNames() {
			body, _ := embeddedBody(n)
			rows = append(rows, []string{n, setup.SkillSummary(body)})
		}
		axi.Render(stdout,
			axi.Table("skills", []string{"name", "use it when"}, rows),
			axi.Help("Run `jig skill <name>` to read one"),
		)
		return 0
	}
	body, ok := embeddedBody(name)
	if !ok {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("jig has no skill named %q", name),
			Code: "VALIDATION_ERROR",
			Help: []string{"Skills this jig carries: " + strings.Join(embeddedNames(), ", ")},
		})
	}
	_, err = stdout.Write(body)
	if err != nil {
		return renderErr(stdout, err)
	}
	return 0
}
