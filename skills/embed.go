// Package skills embeds jig's session skills so they ship inside the jig
// binary: `jig skills install` lays them out on disk without a separate
// skills checkout.
package skills

import "embed"

// FS embeds every skills/<name>/SKILL.md file, rooted at this directory
// (each file is reachable at "<name>/SKILL.md").
//
//go:embed */SKILL.md
var FS embed.FS
