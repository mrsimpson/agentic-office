package agentrunner

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	officememory "github.com/mrsimpson/agentic-office/skills/office-memory"
)

// skill is a skill that the skills injector hands to every harness (dec-skills).
type skill struct {
	name  string
	files fs.FS
}

// agentSkills are the skills that every agent of a tenant needs. The skill office-assistant is
// not among them: it helps a human to connect, set up agents and run meetings, while the agent of
// the runner is already in its meeting.
var agentSkills = []skill{{officememory.Name, officememory.Files()}}

const (
	// skillsDir is the folder of skills in the working directory that Kiro and opencode read.
	skillsDir = ".agent/skills"
	// claudeSkillsDir is the folder of skills that Claude Code reads. The runner links each skill
	// there to its folder in skillsDir, so there is one copy.
	claudeSkillsDir = ".claude/skills"
	// skillMarker is the file that marks a skill folder as written by the office. The runner
	// replaces only folders with this file.
	skillMarker = ".office-skill"
	// excludeHeader introduces the lines that the runner adds to .git/info/exclude.
	excludeHeader = "# Skills that \"office agent run\" writes; they are not part of the project."
)

// markerText is the content of skillMarker.
const markerText = "This skill was written by \"office agent run\" of the agentic office.\n" +
	"The runner replaces this folder at each start. Do not change it: put the rules of your team into the role of the agent.\n"

// injectSkills writes the skills of the agents into the working directory cwd of the harness:
// each skill into .agent/skills/<skill>/, and a symbolic link .claude/skills/<skill> to it. A
// folder of the same name that the office did not write stays as it is, with a warning. When cwd
// lies in a Git work tree, the runner adds what it wrote to .git/info/exclude, so it is not
// committed by accident. A failure is logged; the agent then works without the skill.
func injectSkills(cwd string, logf func(string, ...any)) {
	var written []string
	for _, s := range agentSkills {
		paths, err := injectSkill(cwd, s, logf)
		if err != nil {
			logf("warning: cannot hand the skill %s to the harness: %v", s.name, err)
		}
		written = append(written, paths...)
	}
	if len(written) > 0 {
		if err := excludeFromGit(cwd, written); err != nil {
			logf("warning: cannot add the skills to the Git exclude file: %v", err)
		}
	}
}

// injectSkill writes one skill and its link. It returns the paths it wrote; a path that ends in
// a slash is a folder.
func injectSkill(cwd string, s skill, logf func(string, ...any)) ([]string, error) {
	dir := filepath.Join(cwd, skillsDir, s.name)
	if fi, err := os.Lstat(dir); err == nil {
		if !fi.IsDir() || !isOfficeSkill(dir) {
			logf("warning: %s holds a skill that the office did not write; the runner keeps it and does not hand its own skill %s. Remove the folder to get the skill of the office.", dir, s.name)
			return nil, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := writeSkill(dir, s.files); err != nil {
		return nil, err
	}
	written := []string{dir + string(filepath.Separator)}

	link := filepath.Join(cwd, claudeSkillsDir, s.name)
	target := filepath.Join("..", "..", skillsDir, s.name)
	switch _, err := os.Lstat(link); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return written, err
		}
		if err := os.Symlink(target, link); err != nil && !sameFile(link, dir) {
			return written, err
		}
		written = append(written, link)
	case err != nil:
		return written, err
	case sameFile(link, dir):
		// The link exists, or .claude/skills itself leads to .agent/skills.
		if fi, err := os.Lstat(link); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			written = append(written, link)
		}
	default:
		logf("warning: %s holds a skill that the office did not write; the runner keeps it. Claude Code reads that one instead of %s.", link, dir)
	}
	return written, nil
}

// isOfficeSkill reports whether the folder dir was written by the office.
func isOfficeSkill(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, skillMarker))
	return err == nil && fi.Mode().IsRegular()
}

// sameFile reports whether a and b lead to the same file after all symbolic links.
func sameFile(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	return err == nil && ra == rb
}

// writeSkill writes the files of a skill and the marker into a new folder next to dir and then
// replaces dir with it, so a harness never reads half a skill.
func writeSkill(dir string, files fs.FS) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	err = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			return os.Mkdir(dst, 0o755)
		}
		b, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, skillMarker), []byte(markerText), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another runner in the same folder may have written the skill just now.
		if isOfficeSkill(dir) {
			return nil
		}
		return err
	}
	return nil
}

// excludeFromGit adds the paths to the exclude file of the Git repository of cwd, if cwd lies in
// a Git work tree. It never changes a tracked file such as .gitignore. Without Git, or outside a
// work tree, it does nothing.
func excludeFromGit(cwd string, paths []string) error {
	top, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil // no Git, or not a work tree
	}
	exclude, err := gitOutput(cwd, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return nil
	}
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(cwd, exclude)
	}
	if top, err = filepath.EvalSymlinks(top); err != nil {
		return err
	}
	var want []string
	for _, p := range paths {
		dirSlash := strings.HasSuffix(p, string(filepath.Separator))
		real, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(p)))
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(top, filepath.Join(real, filepath.Base(p)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		line := "/" + path.Clean(filepath.ToSlash(rel))
		if dirSlash {
			line += "/"
		}
		want = append(want, line)
	}
	old, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(old), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add bytes.Buffer
	for _, l := range want {
		if !have[l] {
			add.WriteString(l + "\n")
			have[l] = true
		}
	}
	if add.Len() == 0 {
		return nil
	}
	var b bytes.Buffer
	b.Write(old)
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		b.WriteString("\n")
	}
	if !have[excludeHeader] {
		b.WriteString(excludeHeader + "\n")
	}
	b.Write(add.Bytes())
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	return os.WriteFile(exclude, b.Bytes(), 0o644)
}

// gitOutput runs git in dir and returns its trimmed output.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
