package gitaccess

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mrsimpson/agentic-office/hub/artifacts/check"
)

// SubcommandPreReceive is the first argument that makes the hub program run the rules of a
// push. The hook script that Git access writes calls the hub program with it.
const SubcommandPreReceive = "git-pre-receive"

// The environment that Git access gives to the hook. The values come from the token check and
// the address of the request, never from the client.
const (
	EnvTenant       = "OFFICE_PUSH_TENANT"
	EnvParticipant  = "OFFICE_PUSH_PARTICIPANT"
	EnvName         = "OFFICE_PUSH_NAME"
	EnvMaxFileBytes = "OFFICE_PUSH_MAX_FILE_BYTES"
	EnvMaxFiles     = "OFFICE_PUSH_MAX_FILES"
)

// AuthorEmail is the address of a participant in a commit: the same form that the hub uses for
// its own commits.
func AuthorEmail(participantID string) string { return participantID + "@office.invalid" }

// writeHook writes the hook script into dir. The script starts this program again.
func writeHook(dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("git access: the program of the hook: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("git access: %w", err)
	}
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", `'\''`) + "' " + SubcommandPreReceive + "\n"
	tmp := filepath.Join(dir, ".pre-receive.tmp")
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return fmt.Errorf("git access: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "pre-receive")); err != nil {
		return fmt.Errorf("git access: %w", err)
	}
	return nil
}

// RunPreReceive is the pre-receive hook of the artifact repository. Git starts it before it
// accepts a push, with the new objects in its quarantine area, and gives it one line per
// reference on stdin: "<old> <new> <reference>". A refusal is written to stderr, which the
// participant sees as "remote:" lines, and the exit code is 1. It returns 0 to accept.
func RunPreReceive(stdin io.Reader, stderr io.Writer) int {
	participant, name := os.Getenv(EnvParticipant), os.Getenv(EnvName)
	if participant == "" || os.Getenv(EnvTenant) == "" {
		fmt.Fprintln(stderr, "push refused: the hub did not say who pushes")
		return 1
	}
	maxFile, _ := strconv.ParseInt(os.Getenv(EnvMaxFileBytes), 10, 64)
	maxFiles, _ := strconv.Atoi(os.Getenv(EnvMaxFiles))
	if maxFile <= 0 {
		maxFile = DefaultMaxFileBytes
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	h := &hook{wantName: signature(name), wantEmail: AuthorEmail(participant), maxFile: maxFile, maxFiles: maxFiles}

	var problems []string
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		problems = append(problems, h.checkRef(f[0], f[1], f[2])...)
	}
	if len(problems) == 0 {
		return 0
	}
	fmt.Fprintln(stderr, "push refused, nothing was changed:")
	for _, p := range problems {
		fmt.Fprintln(stderr, "  - "+p)
	}
	return 1
}

type hook struct {
	wantName, wantEmail string
	maxFile             int64
	maxFiles            int
}

func isZero(id string) bool { return strings.Trim(id, "0") == "" }

// signature is a name as Git stores it in a commit: without the characters Git strips.
func signature(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r == '\n' {
			return -1
		}
		return r
	}, s))
}

func (h *hook) checkRef(oldID, newID, ref string) []string {
	if ref != "refs/heads/main" {
		return []string{fmt.Sprintf("only the branch main can be changed, not %s", ref)}
	}
	if isZero(newID) {
		return []string{"the branch main cannot be deleted"}
	}
	rng := newID
	if !isZero(oldID) {
		if _, err := runGit(nil, "merge-base", "--is-ancestor", oldID, newID); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				return []string{"main has changed since your last fetch, or you rewrote its history; run \"git fetch\", merge or rebase your work onto origin/main, and push again. History cannot be rewritten"}
			}
			return []string{"the history could not be checked: " + err.Error()}
		}
		rng = oldID + ".." + newID
	}
	problems := h.checkAuthors(rng)
	return append(problems, h.checkFiles(oldID, newID)...)
}

func (h *hook) checkAuthors(rng string) []string {
	out, err := runGit(nil, "log", "-z", "--format=%H%x1f%an%x1f%ae", rng)
	if err != nil {
		return []string{"the commits could not be read: " + err.Error()}
	}
	var problems []string
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 3 {
			continue
		}
		if signature(f[1]) != h.wantName || f[2] != h.wantEmail {
			problems = append(problems, fmt.Sprintf("commit %s has the author %s <%s>; every commit must have the author %s <%s>. Set user.name and user.email and amend the commits (for example with \"git rebase -r --exec 'git commit --amend --reset-author --no-edit' origin/main\")",
				f[0][:min(10, len(f[0]))], f[1], f[2], h.wantName, h.wantEmail))
		}
	}
	return problems
}

type change struct {
	path, blob string
}

func (h *hook) checkFiles(oldID, newID string) []string {
	base := oldID
	if isZero(oldID) {
		empty, err := runGit(strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return []string{"the files could not be read: " + err.Error()}
		}
		base = strings.TrimSpace(string(empty))
	}
	out, err := runGit(nil, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", base, newID)
	if err != nil {
		return []string{"the files could not be read: " + err.Error()}
	}
	var (
		problems []string
		changes  []change
	)
	parts := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		if len(meta) < 5 {
			break
		}
		newMode, newBlob, status, p := meta[1], meta[3], meta[4], parts[i+1]
		if reason := pathProblem(p); reason != "" {
			problems = append(problems, reason)
			continue
		}
		if status == "D" {
			continue
		}
		if newMode != "100644" && newMode != "100755" {
			problems = append(problems, fmt.Sprintf("%s is not a regular file (mode %s); links and submodules are not allowed", p, newMode))
			continue
		}
		changes = append(changes, change{p, newBlob})
	}
	if len(changes) > h.maxFiles {
		return append(problems, fmt.Sprintf("the push changes %d files; at most %d files may change in one push", len(changes), h.maxFiles))
	}
	return append(problems, h.checkContents(changes)...)
}

// pathProblem returns why a path is not an artifact path, or "".
func pathProblem(p string) string {
	if p == "" || path.IsAbs(p) || strings.Contains(p, `\`) {
		return fmt.Sprintf("the path %q is not a path inside the repository", p)
	}
	for _, el := range strings.Split(p, "/") {
		switch {
		case el == "" || el == "." || el == "..":
			return fmt.Sprintf("the path %q is not a path inside the repository", p)
		case strings.HasPrefix(el, "."):
			return fmt.Sprintf("the path %q has a hidden element %q; hidden files and folders are not allowed", p, el)
		}
	}
	switch path.Ext(p) {
	case ".md", ".yaml", ".yml":
		return ""
	default:
		return fmt.Sprintf("%s: only Markdown (.md) and YAML (.yaml, .yml) files are allowed", p)
	}
}

// checkContents reads the new content of the files with one Git process and checks each file with
// the artifact check. The sizes come first, so that a large file is never read.
func (h *hook) checkContents(changes []change) []string {
	if len(changes) == 0 {
		return nil
	}
	blobs := func(cs []change) *bytes.Reader {
		var in bytes.Buffer
		for _, c := range cs {
			in.WriteString(c.blob + "\n")
		}
		return bytes.NewReader(in.Bytes())
	}
	sizes, err := runGit(blobs(changes), "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return []string{"the files could not be read: " + err.Error()}
	}
	var (
		problems []string
		small    []change
	)
	for i, field := range strings.Fields(string(sizes)) {
		if i >= len(changes) {
			break
		}
		n, _ := strconv.ParseInt(field, 10, 64)
		if n > h.maxFile {
			problems = append(problems, fmt.Sprintf("%s is %d bytes; a file may have at most %d bytes", changes[i].path, n, h.maxFile))
			continue
		}
		small = append(small, changes[i])
	}
	if len(small) == 0 {
		return problems
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Stdin = blobs(small)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return append(problems, err.Error())
	}
	if err := cmd.Start(); err != nil {
		return append(problems, err.Error())
	}
	r := bufio.NewReader(pipe)
	for _, c := range small {
		header, err := r.ReadString('\n')
		if err != nil {
			problems = append(problems, "the files could not be read: "+err.Error())
			break
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			problems = append(problems, fmt.Sprintf("%s: the content is missing", c.path))
			continue
		}
		size, _ := strconv.Atoi(f[2])
		content := make([]byte, size+1) // the content and the line feed that follows it
		if _, err := io.ReadFull(r, content); err != nil {
			problems = append(problems, "the files could not be read: "+err.Error())
			break
		}
		for _, reason := range check.Validate(c.path, content[:size]) {
			problems = append(problems, fmt.Sprintf("%s: %s", c.path, reason))
		}
	}
	_ = cmd.Wait()
	return problems
}

// runGit runs the Git command line tool in the repository that Git started the hook in. The
// quarantine area of the push is in the environment, so Git finds the new objects.
func runGit(stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}
