package contract

// Contract test of the help of the office CLI (if-cli) and of the hub: every command explains
// itself. The test runs the real binaries, needs no hub and checks behaviour only.

import (
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/client/cli/commands"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// helpEnv gives a command no key, no hub and no configuration file.
func helpEnv(t *testing.T) []string {
	return []string{"XDG_CONFIG_HOME=" + t.TempDir(), "OFFICE_HUB=", "OFFICE_KEY=", "OFFICE_CA_CERT=", "OFFICE_TENANT=",
		"OFFICE_HUB_PROFILE=", "OFFICE_SESSION=", "OFFICE_JSON=", "OFFICE_RUNTIME_DIR=" + t.TempDir()}
}

// TestHelpOfEveryCommand: every command answers --help with its purpose, a usage line and an
// example, and "office help <command>" prints the same.
func TestHelpOfEveryCommand(t *testing.T) {
	env := helpEnv(t)
	names := commands.Names()
	if len(names) < 50 {
		t.Fatalf("only %d commands have help", len(names))
	}
	overview, _, code := contracttest.Run(t, "office", env, "help")
	if code != 0 {
		t.Fatalf("office help: exit %d", code)
	}
	for _, name := range names {
		words := strings.Fields(name)
		if name != "help" && name != "version" && !strings.Contains(overview, "  "+name+" ") {
			t.Errorf("office help does not list %q", name)
		}
		out, errOut, code := contracttest.Run(t, "office", env, append(append([]string{}, words...), "--help")...)
		if code != 0 {
			t.Errorf("office %s --help: exit %d: %s", name, code, errOut)
			continue
		}
		if !strings.HasPrefix(out, "office "+name+": ") {
			t.Errorf("office %s --help: no purpose in the first line: %q", name, firstLine(out))
		}
		if !strings.Contains(out, "\nusage: office "+words[0]) {
			t.Errorf("office %s --help: no usage line:\n%s", name, out)
		}
		_, examples, found := strings.Cut(out, "\nExamples:\n")
		if !found || !strings.Contains(examples, "office "+words[0]) {
			t.Errorf("office %s --help: no example that runs the command:\n%s", name, out)
		}
		again, _, code := contracttest.Run(t, "office", env, append([]string{"help"}, words...)...)
		if name != "help" && (code != 0 || again != out) {
			t.Errorf("office help %s differs from office %s --help (exit %d)", name, name, code)
		}
	}
}

// TestHelpTopics: the topics are listed in "office help" and print text.
func TestHelpTopics(t *testing.T) {
	env := helpEnv(t)
	overview, _, _ := contracttest.Run(t, "office", env, "help")
	for _, topic := range commands.TopicNames() {
		if !strings.Contains(overview, topic) {
			t.Errorf("office help does not name the topic %q", topic)
		}
		out, _, code := contracttest.Run(t, "office", env, "help", topic)
		if code != 0 || len(out) < 200 {
			t.Errorf("office help %s: exit %d, %d bytes", topic, code, len(out))
		}
	}
	// An unknown command is a usage error that points to the help.
	if _, errOut, code := contracttest.Run(t, "office", env, "help", "no-such-command"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("office help no-such-command: exit %d: %s", code, errOut)
	}
}

// TestHubHelp: office-hub explains its flags, and "keygen", with an example.
func TestHubHelp(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		out, _, code := contracttest.Run(t, "office-hub", nil, args...)
		if code != 0 {
			t.Fatalf("office-hub %v: exit %d", args, code)
		}
		for _, want := range []string{"usage:", "office-hub keygen FILE", "-signing-key", "-first-system-admin", "-archive-interval", "Example"} {
			if !strings.Contains(out, want) {
				t.Errorf("office-hub %v: no %q in:\n%s", args, want, out)
			}
		}
	}
	for _, args := range [][]string{{"keygen", "-h"}, {"keygen", "--help"}} {
		out, _, code := contracttest.Run(t, "office-hub", nil, args...)
		if code != 0 || !strings.Contains(out, "usage: office-hub keygen FILE") || !strings.Contains(out, "Example:") {
			t.Errorf("office-hub %v: exit %d:\n%s", args, code, out)
		}
	}
	// A wrong flag is a usage error that shows the help on stderr.
	if _, errOut, code := contracttest.Run(t, "office-hub", nil, "-no-such-flag"); code != 2 || !strings.Contains(errOut, "usage:") {
		t.Errorf("office-hub -no-such-flag: exit %d: %s", code, errOut)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
