package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// elicitSchema is the form the person is shown: one text field.
var elicitSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"answer": map[string]any{
			"type":        "string",
			"title":       "Answer",
			"description": "Your answer. It is recorded as the answer of the person.",
		},
	},
	"required": []string{"answer"},
}

// ask is the handler of the ask tool. It records the question the way
// `jig ask` does, which parks the slice on it. When the client can put the
// question to the person it then does, and on accept records the person's
// answer with `jig answer --as person` and returns it; on decline or cancel
// the question stays open, as it does when the client cannot ask at all or
// fails to: the record is the fallback, and it is never lost.
func (tk *toolkit) ask(t Tool) func(context.Context, Client, Args) (Result, error) {
	return func(ctx context.Context, c Client, a Args) (Result, error) {
		inv, err := t.invocation(a)
		if err != nil {
			return Result{}, err
		}
		out, code, err := tk.exec(ctx, inv, "")
		if err != nil {
			return Result{}, err
		}
		if code != 0 {
			return Result{Text: orNothing(out), IsError: true}, nil
		}
		asked := blockFields(out, "asked")
		ticket, slice, qid := asked["ticket"], asked["slice"], asked["question"]
		if ticket == "" || slice == "" || qid == "" { // output this code does not know: leave it as it is
			return Result{Text: out}, nil
		}
		if !c.CanElicit() {
			return recorded(out, qid, "the client cannot ask the person here"), nil
		}

		question := a.Str("question")
		res, err := c.Elicit(ctx, fmt.Sprintf("%s, slice %s asks:\n\n%s", ticket, slice, question), elicitSchema)
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			if errors.Is(err, errClosed) {
				return recorded(out, qid, "the client went away before the person answered"), nil
			}
			return recorded(out, qid, "the client could not ask the person: "+err.Error()), nil
		}
		text, _ := res.Content["answer"].(string)
		text = strings.TrimSpace(text)
		switch {
		case res.Action == "accept" && text != "":
		case res.Action == "accept":
			return recorded(out, qid, "the person sent an empty answer"), nil
		case res.Action == "decline":
			return recorded(out, qid, "the person declined to answer now"), nil
		default:
			return recorded(out, qid, "the person dismissed the question"), nil
		}

		ans := invocation{verb: "answer", pos: []string{ticket, qid}, flags: []string{"--as", "person"}, text: []string{text}}
		aout, acode, err := tk.exec(ctx, ans, "")
		if err != nil {
			return Result{}, err
		}
		if acode != 0 {
			return Result{Text: "The person answered, but the answer could not be recorded:\n" + orNothing(aout), IsError: true}, nil
		}
		return Result{Text: answerText(ticket, slice, qid, text)}, nil
	}
}

// answerText is what the asker reads once the person has answered: the shape
// `jig ask --wait` prints, with the answer as the person's.
func answerText(ticket, slice, qid, text string) string {
	body := axi.Block{Header: "text:"}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			line = "  " + line
		} else {
			line = ""
		}
		body.Lines = append(body.Lines, line)
	}
	return strings.Join([]string{
		axi.KV("answer", [][2]string{{"ticket", ticket}, {"slice", slice}, {"question", qid}, {"provenance", "person"}}),
		body.String(),
		axi.Help(fmt.Sprintf("The person answered in the client; carry on with slice %s using this answer", slice)),
	}, "\n") + "\n"
}

// recorded is the result of a question that was recorded and not answered:
// the asked block of `jig ask` with a help line that fits a caller with no
// --wait, saying why nobody answered yet.
func recorded(askOut, qid, why string) Result {
	return Result{Text: withoutHelp(askOut) + "\n" + axi.Help(fmt.Sprintf(
		"%s, so %s stays open and its slice waits on it. Carry on with what it does not block; the person answers with `jig answer <ticket> %s <text>`, and the answer shows under questions in the context tool",
		capitalize(why), qid, qid)) + "\n"}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// blockFields reads the key: value lines of the block labelled label in a
// command output (axi.KV), nil when there is no such block.
func blockFields(out, label string) map[string]string {
	var fields map[string]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == label+":":
			fields = map[string]string{}
		case fields != nil && strings.HasPrefix(line, "  "):
			if k, v, ok := strings.Cut(strings.TrimPrefix(line, "  "), ": "); ok {
				fields[k] = unquote(v)
			}
		case fields != nil:
			return fields
		}
	}
	return fields
}

// unquote undoes axi.Quote enough for the plain values of an id or a state.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = strings.NewReplacer(`\"`, `"`, `\n`, "\n").Replace(v[1 : len(v)-1])
	}
	return v
}

// withoutHelp is a command output without its closing help block, which
// speaks of commands a caller of the tool does not have.
func withoutHelp(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "help[") {
			lines = lines[:i]
			break
		}
	}
	return strings.Join(lines, "\n")
}
