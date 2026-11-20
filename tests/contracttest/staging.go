package contracttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// AreaStateFile returns the path of the state of the staging area of a session in the staging
// repository (dec-staging-repository): the readers, the deadline, whether an objection holds it,
// the names of its files and the state of a release. The session ID must be safe as a file name.
func AreaStateFile(sessionID string) string { return ".areas/" + sessionID + "/area.yaml" }

// AreaState reads the state of the staging area of a session from the staging repository of a
// tenant, or returns nil if the session has no area.
func AreaState(t testing.TB, staging, sessionID string) map[string]any {
	t.Helper()
	out, err := exec.Command("git", "-C", staging, "show", "main:"+AreaStateFile(sessionID)).Output()
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("state of the staging area of %s: %v", sessionID, err)
	}
	return m
}

// StageFile puts a file into the staging area of a session like a part of the hub would: it
// writes the file and records its name in the area. The area must exist (the meeting session
// opens it when the session starts). No part writes the staging area yet (#201, #202), so tests
// stage their files this way.
func StageFile(t testing.TB, staging, sessionID, path, content string) {
	t.Helper()
	Edit(t, staging, "stage "+path, func(work string) {
		state := filepath.Join(work, filepath.FromSlash(AreaStateFile(sessionID)))
		b, err := os.ReadFile(state)
		if err != nil {
			t.Fatalf("the session %s has no staging area: %v", sessionID, err)
		}
		// Edit the YAML tree, so the other fields stay as the hub wrote them.
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil || len(doc.Content) == 0 {
			t.Fatalf("state of the staging area of %s: %v", sessionID, err)
		}
		root := doc.Content[0]
		var names *yaml.Node
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "names" {
				names = root.Content[i+1]
			}
		}
		if names == nil {
			names = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "names"}, names)
		}
		var list []string
		for _, n := range names.Content {
			list = append(list, n.Value)
		}
		if !slices.Contains(list, path) {
			list = append(list, path)
			slices.Sort(list)
		}
		names.Content = nil
		for _, n := range list {
			names.Content = append(names.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: n})
		}
		out, err := yaml.Marshal(&doc)
		if err != nil {
			t.Fatal(err)
		}
		WriteFile(t, work, AreaStateFile(sessionID), string(out))
		WriteFile(t, work, path, content)
	})
}

// NewArtifactGate is a GitGate that closes only the commits in the artifact repository of a
// tenant. The staging and the session-log repositories go on. With it a test cuts a release off
// after the area was closed for its release.
func NewArtifactGate(t testing.TB, tenant string) *GitGate {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "git-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	flag := filepath.Join(dir, "closed")
	// The hub sets GIT_DIR to the repository it works on.
	script := fmt.Sprintf(`#!/bin/sh
case "$GIT_DIR" in */repos/tenants/%[3]s) mine=yes;; *) mine=no;; esac
if [ "$1" = commit-tree ] && [ "$mine" = yes ] && [ -e %[1]q ]; then
  echo "git-gate: commits are closed" >&2
  exit 1
fi
exec %[2]q "$@"
`, flag, real, tenant)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &GitGate{Env: []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, flag: flag}
}

// ReleaseCommits returns the commits of the artifact repository that release a session, newest
// first, as "<author name> <author email>".
func ReleaseCommits(t testing.TB, repo, sessionID string) []string {
	t.Helper()
	out := Git(t, repo, "log", "--format=%an <%ae>", "--grep=^Office-Release: "+sessionID+"$", "main")
	return strings.FieldsFunc(out, func(r rune) bool { return r == '\n' })
}
