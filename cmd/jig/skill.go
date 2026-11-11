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
		Spec:  cmdSpec{"skill", "jig skill <name> [<ref>]: print a skill's instructions, or one of its references, as this jig carries them; with no name, list the skills", nil, false},
		Order: 521,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSkill(e, args, stdout)
		},
	})
}

// cmdSkill implements `jig skill [<name> [<ref>]]`. A skill's text is printed as it is
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
	if len(names) > 2 {
		return renderErr(stdout, &axi.Error{Msg: "skill takes at most one skill name and one reference", Code: "VALIDATION_ERROR", Help: []string{"Run `jig skill <name> [<ref>]`"}})
	}
	var name, ref string
	if len(names) > 0 {
		name = names[0]
	}
	if len(names) > 1 {
		ref = names[1]
	}
	if name == "" {
		var rows [][]string
		for _, n := range embeddedNames() {
			body, _ := embeddedBody(n)
			rows = append(rows, []string{n, setup.SkillSummary(body), strings.Join(embeddedRefs(n), " ")})
		}
		axi.Render(stdout,
			axi.Table("skills", []string{"name", "use it when", "references"}, rows),
			axi.Help("Run `jig skill <name>` to read one, `jig skill <name> <ref>` for a reference"),
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
	if ref != "" {
		text, ok := embeddedRef(name, ref)
		if !ok {
			help := "Skill " + name + " has no references"
			if refs := embeddedRefs(name); len(refs) > 0 {
				help = "References of skill " + name + ": " + strings.Join(refs, ", ")
			}
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("skill %s has no reference named %q", name, ref),
				Code: "VALIDATION_ERROR",
				Help: []string{help},
			})
		}
		body = text
	}
	_, err = stdout.Write(body)
	if err != nil {
		return renderErr(stdout, err)
	}
	return 0
}
