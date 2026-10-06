//go:build windows

package session

import "github.com/develdeco/jig/internal/envrun"

// taskkillPath is envrun's: the taskkill.exe a process-tree kill runs.
func taskkillPath() string { return envrun.TaskkillPath() }
