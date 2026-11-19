package cli

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// memorySkill is the skill that tells an agent how to work with the memory of the team, the wiki
// of the tenant (dec-wiki). It ships with the office; agents read it but never change it.
//
//go:embed skill/office-memory/SKILL.md
var memorySkill string

// memorySkillName is the name of the skill and of its directory.
const memorySkillName = "office-memory"

// SkillInstallResult is the result of "office skill install".
type SkillInstallResult struct {
	Skill string `json:"skill"`
	File  string `json:"file"`
}

// cmdSkillShow prints the skill of the memory.
func cmdSkillShow(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 0 {
			return usagef("usage: office skill show")
		}
		if e.json {
			e.printJSON(map[string]string{"skill": memorySkillName, "text": memorySkill})
			return nil
		}
		_, err := fmt.Fprint(e.stdout, memorySkill)
		return err
	}
}

// cmdSkillInstall writes the skill of the memory into the skills directory of an agent harness.
// It replaces an older copy, so the skill stays the one of this version of the office.
func cmdSkillInstall(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) > 1 {
			return usagef("usage: office skill install [<skills-dir>]")
		}
		dir := filepath.Join(".claude", "skills")
		if len(args) == 1 {
			dir = args[0]
		}
		file := filepath.Join(dir, memorySkillName, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(memorySkill), 0o644); err != nil {
			return err
		}
		res := SkillInstallResult{Skill: memorySkillName, File: file}
		if e.json {
			e.printJSON(res)
			return nil
		}
		_, err := fmt.Fprintf(e.stdout, "installed the skill %s in %s\n", memorySkillName, file)
		return err
	}
}
