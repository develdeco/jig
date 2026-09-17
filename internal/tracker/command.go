package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// commandAdapter shells an operator-supplied command for every tracker
// operation, sending a small JSON envelope on stdin.
type commandAdapter struct {
	cmd string
	dir string
}

func newCommandAdapter(cfg project.Config, st *store.Store) *commandAdapter {
	return &commandAdapter{cmd: cfg.TrackerCmd, dir: st.Root}
}

func (a *commandAdapter) Name() string { return "command" }

// wireMessage is the JSON envelope written to the command's stdin.
type wireMessage struct {
	Action  string `json:"action"`
	Ticket  string `json:"ticket"`
	Payload any    `json:"payload"`
}

func (a *commandAdapter) call(action, ticket string, payload any) (string, error) {
	msg := wireMessage{Action: action, Ticket: ticket, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("tracker: command %s: marshal payload: %w", action, err)
	}
	out, err := runShell(a.cmd, a.dir, data)
	if err != nil {
		return "", fmt.Errorf("tracker: command %s: %w", action, err)
	}
	return out, nil
}

func (a *commandAdapter) Mint(d Draft) (string, error) {
	out, err := a.call("mint", "", d)
	if err != nil {
		return "", err
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return "", fmt.Errorf("tracker: command mint: parse stdout %q: %w", out, err)
	}
	if res.ID == "" {
		return "", fmt.Errorf("tracker: command mint: no id in output %q", out)
	}
	return res.ID, nil
}

func (a *commandAdapter) Project(ticketID string, p Projection) error {
	_, err := a.call("project", ticketID, p)
	return err
}

func (a *commandAdapter) Comment(ticketID string, body string) error {
	_, err := a.call("comment", ticketID, map[string]string{"body": body})
	return err
}

// runShell runs command via the platform shell (cmd /C on Windows, sh -c
// elsewhere), writing stdin to it and returning trimmed stdout.
func runShell(command, dir string, stdin []byte) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%q: %s", command, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
