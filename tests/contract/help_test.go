package contract

// Contract test of the help of the office CLI (if-cli) and of the hub: every command explains
// itself. The test runs the real binaries, needs no hub and checks behaviour only.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// helpEnv gives a command no key, no hub and no configuration file.
func helpEnv(t *testing.T) []string {
	return []string{"XDG_CONFIG_HOME=" + t.TempDir(), "OFFICE_HUB=", "OFFICE_KEY=", "OFFICE_CA_CERT=", "OFFICE_TENANT=",
		"OFFICE_HUB_PROFILE=", "OFFICE_SESSION=", "OFFICE_JSON=", "OFFICE_RUNTIME_DIR=" + t.TempDir()}
}

// commandLine is a command in the list of "office help": two spaces, the command, two or more
// spaces, its purpose.
var commandLine = regexp.MustCompile(`^  (\S+(?: \S+){0,2}) {2,}\S`)

// listedCommands returns the commands and the topics that "office help" lists.
func listedCommands(t *testing.T, env []string) (commands, topics []string, overview string) {
	t.Helper()
	overview, _, code := contracttest.Run(t, "office", env, "help")
	if code != 0 {
		t.Fatalf("office help: exit %d", code)
	}
	for _, line := range strings.Split(overview, "\n") {
		if _, list, ok := strings.Cut(line, "a topic: "); ok {
			topics = strings.Split(list, ", ")
			continue
		}
		if m := commandLine.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "office ") {
			commands = append(commands, m[1])
		}
	}
	return commands, topics, overview
}

// TestHelpOfEveryCommand: every command that "office help" lists answers --help with its purpose,
// a usage line and an example, and "office help <command>" prints the same.
func TestHelpOfEveryCommand(t *testing.T) {
	env := helpEnv(t)
	names, _, _ := listedCommands(t, env)
	if len(names) < 50 {
		t.Fatalf("office help lists only %d commands: %v", len(names), names)
	}
	for _, want := range []string{"key create", "meeting join", "agent run", "hub add", "config show", "task update", "help", "version"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("office help does not list %q", want)
		}
	}
	for _, name := range names {
		words := strings.Fields(name)
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

// TestHelpOfEveryGroup: every start of a command, such as "meeting" or "admin tenant", explains
// itself. "--help" and "office help <group>" list the commands below it with exit 0; the group
// alone lists them on stderr as a usage error. A command that is also a group ("sync") lists
// the commands below it in its own help. A topic of the same name ("config") wins in
// "office help <group>".
func TestHelpOfEveryGroup(t *testing.T) {
	env := helpEnv(t)
	names, topics, _ := listedCommands(t, env)
	isTopic := map[string]bool{}
	for _, tp := range topics {
		isTopic[tp] = true
	}
	isCommand := map[string]bool{}
	for _, n := range names {
		isCommand[n] = true
	}
	below := map[string][]string{}
	for _, n := range names {
		words := strings.Fields(n)
		for i := 1; i < len(words); i++ {
			prefix := strings.Join(words[:i], " ")
			below[prefix] = append(below[prefix], n)
		}
	}
	for _, want := range []string{"meeting", "admin", "admin tenant", "key", "sync"} {
		if len(below[want]) == 0 {
			t.Errorf("no commands below %q", want)
		}
	}
	for prefix, subs := range below {
		words := strings.Fields(prefix)
		out, errOut, code := contracttest.Run(t, "office", env, append(append([]string{}, words...), "--help")...)
		if code != 0 {
			t.Errorf("office %s --help: exit %d: %s", prefix, code, errOut)
			continue
		}
		for _, s := range subs {
			if !strings.Contains(out, "  "+s+" ") {
				t.Errorf("office %s --help does not list %q:\n%s", prefix, s, out)
			}
		}
		if isCommand[prefix] {
			continue
		}
		if again, _, code := contracttest.Run(t, "office", env, append([]string{"help"}, words...)...); !isTopic[prefix] && (code != 0 || again != out) {
			t.Errorf("office help %s differs from office %s --help (exit %d)", prefix, prefix, code)
		}
		if _, errOut, code := contracttest.Run(t, "office", env, words...); code != 2 || !strings.Contains(errOut, "  "+subs[0]+" ") {
			t.Errorf("office %s: exit %d, want 2 and the list of commands on stderr: %s", prefix, code, errOut)
		}
	}
}

// TestHelpSuggestsCommands: a mistyped command names the command that was probably meant.
func TestHelpSuggestsCommands(t *testing.T) {
	env := helpEnv(t)
	for typed, want := range map[string]string{"meetng join": "meeting join", "join": "meeting join",
		"admin tenat list": "admin tenant list"} {
		_, errOut, code := contracttest.Run(t, "office", env, strings.Fields(typed)...)
		if code != 2 || !strings.Contains(errOut, `did you mean "`+want+`"`) {
			t.Errorf("office %s: exit %d, want a suggestion of %q: %s", typed, code, want, errOut)
		}
	}
	// The error names what the user typed, not the --help that "office help" adds.
	if _, errOut, _ := contracttest.Run(t, "office", env, "help", "no-such-command"); strings.Contains(errOut, "--help") {
		t.Errorf("office help no-such-command: names --help: %s", errOut)
	}
}

// TestHelpTopics: the topics are listed in "office help" and print text.
func TestHelpTopics(t *testing.T) {
	env := helpEnv(t)
	_, topics, _ := listedCommands(t, env)
	if len(topics) < 5 {
		t.Fatalf("office help lists only the topics %v", topics)
	}
	for _, topic := range topics {
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
