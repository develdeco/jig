// Package skills embeds jig's session skills so they ship inside the jig
// binary: `jig skills install` lays them out on disk without a separate
// skills checkout.
package skills

import "embed"

// FS embeds every skills/<name>/SKILL.md file and every reference beside it,
// rooted at this directory (reachable at "<name>/SKILL.md" and
// "<name>/references/<ref>.md").
//
//go:embed */SKILL.md */references/*.md
var FS embed.FS
