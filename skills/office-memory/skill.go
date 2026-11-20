// Package officememory is the interface Office memory skill (if-office-memory-skill in
// docs/architecture/05-building-blocks.arc42.md) of the building block Skills (bb-skills).
//
// The skill itself is the Markdown file SKILL.md in this folder, in the Agent Skills format. A
// human installs it with "npx skills add mrsimpson/agentic-office --skill office-memory". This
// package embeds the same files, so the agent runner can hand the skill to a harness. This Go
// file is not part of the skill.
package officememory

import (
	"embed"
	"io/fs"
)

// Name is the name of the skill and of its folder.
const Name = "office-memory"

//go:embed SKILL.md
var files embed.FS

// Files returns the files of the skill, with SKILL.md at the root.
func Files() fs.FS { return files }
