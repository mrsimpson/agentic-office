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
	EnvAgent        = "OFFICE_PUSH_AGENT" // "1" when the participant is an agent
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
// participant sees as "remote:" lines, and the exit code is 1. It returns 0 to accept. Hints,
// such as a link of a document that leads nowhere, go to stderr too; they never refuse a push.
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
	h := &hook{wantName: signature(name), wantEmail: AuthorEmail(participant), maxFile: maxFile, maxFiles: maxFiles,
		agent: os.Getenv(EnvAgent) == "1"}

	var problems, hints []string
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		p, hs := h.checkRef(f[0], f[1], f[2])
		problems, hints = append(problems, p...), append(hints, hs...)
	}
	if len(hints) > 0 {
		fmt.Fprintln(stderr, "hints, which do not refuse the push:")
		for _, hint := range hints {
			fmt.Fprintln(stderr, "  - "+hint)
		}
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
	agent               bool // the participant is an agent: no agent changes a file that the owner of its schema keeps from agents (dec-team-agents)
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

// checkRef checks the push of one reference. It returns the problems, which refuse the push, and
// the hints, which do not.
func (h *hook) checkRef(oldID, newID, ref string) (problems, hints []string) {
	if ref != "refs/heads/main" {
		return []string{fmt.Sprintf("only the branch main can be changed, not %s", ref)}, nil
	}
	if isZero(newID) {
		return []string{"the branch main cannot be deleted"}, nil
	}
	rng := newID
	if !isZero(oldID) {
		if _, err := runGit(nil, "merge-base", "--is-ancestor", oldID, newID); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				return []string{"main has changed since your last fetch, or you rewrote its history; run \"git fetch\", merge or rebase your work onto origin/main, and push again. History cannot be rewritten"}, nil
			}
			return []string{"the history could not be checked: " + err.Error()}, nil
		}
		rng = oldID + ".." + newID
	}
	problems = h.checkAuthors(rng)
	files, hints := h.checkFiles(oldID, newID)
	return append(problems, files...), hints
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

// checkFiles checks the files that the push changes. It returns the problems and the hints of
// the files.
func (h *hook) checkFiles(oldID, newID string) ([]string, []string) {
	base := oldID
	if isZero(oldID) {
		empty, err := runGit(strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return []string{"the files could not be read: " + err.Error()}, nil
		}
		base = strings.TrimSpace(string(empty))
	}
	out, err := runGit(nil, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", base, newID)
	if err != nil {
		return []string{"the files could not be read: " + err.Error()}, nil
	}
	var (
		problems []string
		changes  []change
		olds     []change // the old state of every path that the push changes or removes
	)
	parts := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		if len(meta) < 5 {
			break
		}
		oldBlob, newMode, newBlob, status, p := meta[2], meta[1], meta[3], meta[4], parts[i+1]
		if !isZero(oldBlob) {
			olds = append(olds, change{p, oldBlob})
		}
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
	problems = append(problems, h.checkStubs(olds)...)
	if h.agent {
		problems = append(problems, h.checkNoAgents(olds, changes)...)
	}
	if len(changes) > h.maxFiles {
		return append(problems, fmt.Sprintf("the push changes %d files; at most %d files may change in one push", len(changes), h.maxFiles)), nil
	}
	contents, hints := h.checkContents(newID, changes)
	return append(problems, contents...), hints
}

// checkStubs refuses every change or removal of a path whose old file is the stub of a staged
// artifact. The artifact check decides what a stub is. A file larger than a file may be is never
// read, because no stub is that large.
func (h *hook) checkStubs(olds []change) []string {
	blobs, err := h.readBlobs(olds)
	if err != nil {
		return []string{"the files could not be read: " + err.Error()}
	}
	var problems []string
	for i, c := range olds {
		if blobs[i].content != nil && check.IsStub(c.path, blobs[i].content) {
			problems = append(problems, fmt.Sprintf("%s is the stub of an artifact that waits for the release of its session; a push cannot change or remove it. Fetch, restore the file as it is on origin/main, and push again", c.path))
		}
	}
	return problems
}

// checkNoAgents refuses the push of an agent that changes, creates or removes a file that no agent
// may change, such as the definition of a team agent (dec-team-agents). The old state decides for a
// change or a removal, the new state for a file that is new or that now names the schema. The
// artifact check decides what such a file is.
func (h *hook) checkNoAgents(olds, changes []change) []string {
	var problems []string
	seen := map[string]bool{}
	for _, cs := range [][]change{olds, changes} {
		blobs, err := h.readBlobs(cs)
		if err != nil {
			return append(problems, "the files could not be read: "+err.Error())
		}
		for i, c := range cs {
			if !seen[c.path] && blobs[i].content != nil && check.NoAgents(c.path, blobs[i].content) {
				seen[c.path] = true
				problems = append(problems, fmt.Sprintf("%s: no agent may change this file; a member changes it with their own key", c.path))
			}
		}
	}
	return problems
}

// blob is a file as readBlobs read it: its size, and its content when it was read.
type blob struct {
	size    int64
	content []byte // nil when the file is larger than a file may be, or missing
}

// readBlobs reads the files of cs with one Git process for the sizes and one for the contents. The
// sizes come first, so a file larger than a file may be is never read.
func (h *hook) readBlobs(cs []change) ([]blob, error) {
	out := make([]blob, len(cs))
	if len(cs) == 0 {
		return out, nil
	}
	var in bytes.Buffer
	for _, c := range cs {
		in.WriteString(c.blob + "\n")
	}
	sizes, err := runGit(bytes.NewReader(in.Bytes()), "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return nil, err
	}
	var small []int
	for i, field := range strings.Fields(string(sizes)) {
		if i >= len(cs) {
			break
		}
		out[i].size, _ = strconv.ParseInt(field, 10, 64)
		if out[i].size <= h.maxFile {
			small = append(small, i)
		}
	}
	if len(small) == 0 {
		return out, nil
	}
	in.Reset()
	for _, i := range small {
		in.WriteString(cs[i].blob + "\n")
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Stdin = bytes.NewReader(in.Bytes())
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// After a read error the process may still wait to write; stop it, so that Wait returns.
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	r := bufio.NewReader(pipe)
	for _, i := range small {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			continue // the object is missing
		}
		size, _ := strconv.Atoi(f[2])
		content := make([]byte, size+1) // the content and the line feed that follows it
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, err
		}
		out[i].content = content[:size]
	}
	return out, nil
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

// checkContents reads the new content of the files and checks each file with the artifact check.
// A new file with the content of a stub is refused: only the hub writes stubs. The hints of a
// valid file come from the artifact check too; a link resolves in the new state of main (newID),
// so links between the files of one push work.
func (h *hook) checkContents(newID string, changes []change) (problems, hints []string) {
	blobs, err := h.readBlobs(changes)
	if err != nil {
		return []string{"the files could not be read: " + err.Error()}, nil
	}
	tree := newTreeIndex(newID)
	for i, c := range changes {
		switch b := blobs[i]; {
		case b.size > h.maxFile:
			problems = append(problems, fmt.Sprintf("%s is %d bytes; a file may have at most %d bytes", c.path, b.size, h.maxFile))
		case b.content == nil:
			problems = append(problems, fmt.Sprintf("%s: the content is missing", c.path))
		case check.IsStub(c.path, b.content):
			problems = append(problems, fmt.Sprintf("%s has the content of the stub of an artifact; only the hub writes stubs", c.path))
		default:
			for _, reason := range check.Validate(c.path, b.content) {
				problems = append(problems, fmt.Sprintf("%s: %s", c.path, reason))
			}
			for _, hint := range check.Hints(c.path, b.content, tree.exists) {
				hints = append(hints, fmt.Sprintf("%s: %s", c.path, hint))
			}
		}
	}
	return problems, hints
}

// treeIndex knows the files and directories of one commit. It lists them on the first question.
type treeIndex struct {
	commit string
	paths  map[string]bool // every file and every directory that holds a file
	err    error
}

func newTreeIndex(commit string) *treeIndex { return &treeIndex{commit: commit} }

// exists reports whether p is a file or a directory with files in the commit. When the commit
// cannot be listed, every path exists, so no hint is wrong.
func (t *treeIndex) exists(p string) bool {
	if t.paths == nil && t.err == nil {
		out, err := runGit(nil, "ls-tree", "-r", "-z", "--name-only", "--full-tree", t.commit)
		if err != nil {
			t.err = err
		} else {
			t.paths = map[string]bool{}
			for _, f := range strings.Split(string(out), "\x00") {
				for d := f; d != "" && d != "." && !t.paths[d]; d = path.Dir(d) {
					t.paths[d] = true
				}
			}
		}
	}
	return t.err != nil || t.paths[p]
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
