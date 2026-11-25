package commands

import (
	"sort"
	"strings"
)

// Help is the help of one command: what "office <command> --help" and "office help <command>"
// print, together with the flags of the command. Every command has a purpose, a synopsis and at
// least one example. A contract test checks this for every name of Names.
type Help struct {
	// Name is the command as typed, one to three words, for example "meeting join".
	Name string
	// Group is the heading of the command in "office help".
	Group string
	// Summary is one line: what the command does.
	Summary string
	// Usage is the synopsis after "office ", with the arguments and the main flags.
	Usage string
	// Details are paragraphs of plain text: rules, defaults, who may call it, the form of --json.
	Details string
	// Examples are shell lines, each a complete command. A line may end in a comment.
	Examples []string
}

// Groups of the commands, in the order of "office help".
const (
	groupKeys     = "Keys and identity"
	groupAdmin    = "Administration (system admin or tenant admin)"
	groupHubs     = "Hubs and configuration"
	groupMeeting  = "Meeting"
	groupEars     = "Speech and vocabulary"
	groupStage    = "Stage, portal, slides and documents"
	groupRecords  = "Decisions, notes, minutes and tasks"
	groupSync     = "Artifacts in a local directory"
	groupImport   = "Import"
	groupAgents   = "Agents"
	groupInternal = "Other"
)

// Names returns the names of all commands that have help, sorted.
func Names() []string {
	names := make([]string, 0, len(helpTable))
	for _, h := range helpTable {
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns the help of a command.
func Lookup(name string) (Help, bool) {
	for _, h := range helpTable {
		if h.Name == name {
			return h, true
		}
	}
	return Help{}, false
}

// Subcommands returns the help of the commands below a prefix of one or two words, for example
// "meeting" or "admin tenant", sorted by name. The prefix itself is not part of the result.
func Subcommands(prefix string) []Help {
	var hs []Help
	for _, h := range helpTable {
		if strings.HasPrefix(h.Name, prefix+" ") {
			hs = append(hs, h)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Name < hs[j].Name })
	return hs
}

// GroupHelp is what "office <prefix> --help" prints for a prefix that is not a command itself:
// the commands below it, each with its purpose.
func GroupHelp(prefix string) string {
	var b strings.Builder
	b.WriteString("usage: office " + prefix + " <command> [flags]\n\nCommands:\n")
	b.WriteString(CommandList(Subcommands(prefix)))
	b.WriteString("\nRun \"office " + prefix + " <command> --help\" for the purpose, arguments, flags and an example.\n")
	return b.String()
}

// CommandList prints commands as lines of the overview: the name and its purpose.
func CommandList(hs []Help) string {
	var b strings.Builder
	for _, h := range hs {
		b.WriteString("  " + h.Name + strings.Repeat(" ", max(1, 27-len(h.Name))) + h.Summary + "\n")
	}
	return b.String()
}

// Suggest returns the commands that are probably meant by a mistyped command: the names that
// end with the typed words ("join" → "meeting join"), else the closest names within a small
// edit distance ("meetng join" → "meeting join").
func Suggest(typed string) []string {
	var suffix, closest []string
	best := 1 + len(typed)/5
	for _, name := range Names() {
		if strings.HasSuffix(name, " "+typed) {
			suffix = append(suffix, name)
		}
		switch d := levenshtein(typed, name); {
		case d < best:
			best, closest = d, []string{name}
		case d == best:
			closest = append(closest, name)
		}
	}
	if len(suffix) > 0 {
		return suffix
	}
	return closest
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Overview is what "office help" prints: every command with its purpose, grouped, and where to
// find more.
func Overview() string {
	var b strings.Builder
	b.WriteString("usage: office <command> [flags]\n")
	b.WriteString("\nThe office client for humans and agents. Every command explains itself:\n")
	b.WriteString("  office <command> --help        purpose, arguments, flags and an example\n")
	b.WriteString("  office help <command>          the same\n")
	b.WriteString("  office <group> --help          the commands of a group, for example: office admin --help\n")
	b.WriteString("  office help <topic>            a topic: " + strings.Join(TopicNames(), ", ") + "\n")
	var groups []string
	byGroup := map[string][]Help{}
	for _, h := range helpTable {
		if _, ok := byGroup[h.Group]; !ok {
			groups = append(groups, h.Group)
		}
		byGroup[h.Group] = append(byGroup[h.Group], h)
	}
	sort.Slice(groups, func(i, j int) bool { return groupRank(groups[i]) < groupRank(groups[j]) })
	for _, g := range groups {
		hs := byGroup[g]
		sort.Slice(hs, func(i, j int) bool { return hs[i].Name < hs[j].Name })
		b.WriteString("\n" + g + ":\n")
		b.WriteString(CommandList(hs))
	}
	b.WriteString("\nThe user guide (USER_GUIDE.md in the repository) shows how to set up a hub, join a\n")
	b.WriteString("meeting and bring in an agent.\n")
	b.WriteString("\nExit codes: 0 success, 1 error, 2 usage error. \"office help json\" describes --json.\n")
	return b.String()
}

func groupRank(g string) int {
	for i, x := range []string{groupKeys, groupHubs, groupMeeting, groupEars, groupStage, groupRecords, groupImport,
		groupAgents, groupAdmin, groupInternal} {
		if x == g {
			return i
		}
	}
	return 99
}

// helpTable has the help of every command of the CLI, in no order.
var helpTable = buildHelp([]Help{
	// Keys and identity.
	{Name: "key create", Group: groupKeys, Summary: "create a key pair and print the public key",
		Usage: "key create",
		Details: `Creates the private key of this participant if there is none, and prints the public key.
An existing key is never changed.
The key file is <config>/agentic-office/client/key.pem, or the file of --key (OFFICE_KEY).
<config> is $XDG_CONFIG_HOME if it is set to an absolute path, else ~/.config on Linux and
~/Library/Application Support on macOS. The private key never leaves your computer. Send the public key to an admin, who
registers it. Several participants on one computer, for example a human and an agent, use
one key file each.`,
		Examples: []string{"office key create", "office key create --key ~/agents/bot/key.pem   # a key for an agent"}},
	{Name: "key show", Group: groupKeys, Summary: "print the public key and the key ID",
		Usage:    "key show",
		Details:  "Prints the public key and the key ID of the key file again, for example to send it to an admin.",
		Examples: []string{"office key show"}},
	{Name: "whoami", Group: groupKeys, Summary: "authenticate and print your participant ID",
		Usage: "whoami",
		Details: `Signs in to the hub with your key and prints the participant ID that the hub knows you by.
It fails with an error that names what is missing: the key file, the hub address or its
certificate (see "office hub add"), or the registration of your public key.`,
		Examples: []string{"office whoami", "office whoami --hub-profile lab   # the same, at the hub of the profile lab"}},

	// Administration.
	{Name: "admin user create", Group: groupAdmin, Summary: "register a user with its first public key",
		Usage: "admin user create <user> <public-key> --name NAME --kind human|agent",
		Details: `Only a system admin creates users. The user ID, for example "ada", is also the participant
ID in every session. Registering a user does not give access to a tenant: add the user with
"office admin member set". A system admin is never a member of a tenant, so use another user
for your work in a tenant. Removing a user ends its validity and keeps the record, so an ID
is never used again.`,
		Examples: []string{`office admin user create ada "$PUBLIC_KEY" --name "Ada Lovelace" --kind human`}},
	{Name: "admin user list", Group: groupAdmin, Summary: "list all users, also the removed ones",
		Usage: "admin user list", Details: "Only a system admin may call it.", Examples: []string{"office admin user list"}},
	{Name: "admin user remove", Group: groupAdmin, Summary: "end a user and its memberships",
		Usage:    "admin user remove <user>",
		Details:  "Only a system admin may call it. The record stays; the user ID is never used again.",
		Examples: []string{"office admin user remove ada"}},
	{Name: "admin system-admin add", Group: groupAdmin, Summary: "make a human user a system admin",
		Usage: "admin system-admin add <user>",
		Details: `Only a system admin may call it. The first system admin comes from the hub flag
-first-system-admin at the first start of the hub. A system admin is never a member of a
tenant.`,
		Examples: []string{"office admin system-admin add root2"}},
	{Name: "admin system-admin remove", Group: groupAdmin, Summary: "take the system admin role away",
		Usage:    "admin system-admin remove <user>",
		Details:  "Only a system admin may call it. The hub always keeps at least one system admin.",
		Examples: []string{"office admin system-admin remove root2"}},
	{Name: "admin tenant create", Group: groupAdmin, Summary: "create a tenant and name its first tenant admin",
		Usage: "admin tenant create <tenant> <first-tenant-admin> [--name NAME]",
		Details: `Only a system admin may call it. A tenant is one organisation with its own repository: its
sessions, decisions, minutes, notes, tasks and slide decks are visible only to its members.
The first tenant admin must be a user who is not a system admin. --name is the display name
(default: the tenant ID).`,
		Examples: []string{`office admin tenant create acme ada --name "Acme Corp"`}},
	{Name: "admin tenant list", Group: groupAdmin, Summary: "list the tenants",
		Usage: "admin tenant list", Details: "Only a system admin may call it.", Examples: []string{"office admin tenant list"}},
	{Name: "admin tenant remove", Group: groupAdmin, Summary: "end a tenant",
		Usage: "admin tenant remove <tenant>",
		Details: `Only a system admin may call it. The record and the repository of the tenant stay; the tenant
ends its validity.`,
		Examples: []string{"office admin tenant remove acme"}},
	{Name: "admin member set", Group: groupAdmin, Summary: "add a user to a tenant or change its role",
		Usage: "admin member set <tenant> <user> [--role member|tenant-admin]",
		Details: `A system admin or a tenant admin of the tenant may call it. The default role is member. A
tenant always keeps at least one tenant admin. A system admin cannot be a member.`,
		Examples: []string{"office admin member set acme bob", "office admin member set acme bob --role tenant-admin"}},
	{Name: "admin member remove", Group: groupAdmin, Summary: "end the membership of a user in a tenant",
		Usage:    "admin member remove <tenant> <user>",
		Details:  "A system admin or a tenant admin of the tenant may call it. The record stays.",
		Examples: []string{"office admin member remove acme bob"}},
	{Name: "admin member list", Group: groupAdmin, Summary: "list the members of a tenant",
		Usage:    "admin member list <tenant>",
		Details:  "A system admin or a member of the tenant may call it. A tenant that you are not a member of looks as if it did not exist (not_found).",
		Examples: []string{"office admin member list acme"}},
	{Name: "admin key add", Group: groupAdmin, Summary: "add a public key to a user",
		Usage:    "admin key add <user> <public-key>",
		Details:  "You may add a key to yourself; a system admin may add a key to any user. A user can have several keys.",
		Examples: []string{`office admin key add ada "$PUBLIC_KEY"`}},
	{Name: "admin key revoke", Group: groupAdmin, Summary: "revoke a key; its tokens stop working at once",
		Usage:    "admin key revoke <key-id>",
		Details:  "The key ID is the one that \"office key show\" prints. The last key of a user cannot be revoked.",
		Examples: []string{"office admin key revoke SHA256:abc123"}},

	// Hubs and configuration.
	{Name: "hub add", Group: groupHubs, Summary: "add a hub profile and trust the certificate of the hub",
		Usage: "hub add <name> <url> [--tenant T] [--key FILE] [--default] [--fingerprint F]",
		Details: `Reads the certificate of the hub, shows its SHA-256 fingerprint and asks you to confirm it.
Compare the fingerprint with the one in the log of the hub. With --fingerprint (hexadecimal,
with or without colons or a "sha256:" prefix) the command compares instead of asking, so it
works in scripts; a wrong fingerprint writes nothing. The command stores the certificate in
<config>/agentic-office/client/hubs/<name>.crt and writes the profile to the user file
config.yaml, keeping its comments. A hub with a certificate from an authority that this
computer trusts needs no stored certificate and no question. A profile of the same name is
replaced. --default makes it the profile that is used when you name none.`,
		Examples: []string{"office hub add main https://hub.example.org:8740 --tenant acme --default",
			"office hub add lab https://lab.example.org:8740 --tenant research --fingerprint 3f:9a:...   # in a script"}},
	{Name: "hub list", Group: groupHubs, Summary: "list the hub profiles",
		Usage:    "hub list",
		Details:  "Lists the profiles of the user file and the project file. The active profile has a star.",
		Examples: []string{"office hub list"}},
	{Name: "hub remove", Group: groupHubs, Summary: "remove a hub profile and its stored certificate",
		Usage: "hub remove <name>",
		Details: `Removes the profile from the user file, clears hub-profile if it names this profile, and
deletes the certificate that "hub add" stored. A profile of the project file you edit by hand.`,
		Examples: []string{"office hub remove lab"}},
	{Name: "config show", Group: groupHubs, Summary: "show the effective settings and where each comes from",
		Usage: "config show",
		Details: `Prints the settings of the configuration files, the defaults and the environment, with the
source of each value, the active hub profile, and whether the key variable of the decision
model is set (never the key). The flags of the connection (--hub-profile, --hub, --tenant,
--ca-cert, --key) are part of it; the other flags override what it shows. Run it in the folder
where you start your commands, because the project file .office/config.yaml counts.
"office help config" describes the files, the order and the keys.

--json prints {"files": [{"kind", "path", "found"}], "profile", "settings": [{"key", "value",
"source", "from", "profile", "unused"}], "keySet"}. "source" is default, user, project, env,
flag or profile; "profile" is null when no profile is active; "unused" marks a wake setting
that the effective strategy does not use.`,
		Examples: []string{"office config show", "office config show --json"}},

	// Meeting.
	{Name: "meeting join", Group: groupMeeting, Summary: "join a meeting, start the local log and print its path",
		Usage: "meeting join <session> [--name N] [--foreground] [--mic | --wav FILE] [ears flags]",
		Details: `Joins the session (it exists as soon as someone joins), starts the log replicator and prints
the path of your local log: <runtime dir>/<session>/<participant>.jsonl, one compact JSON line
per event, append-only. A participant joins once; joining again, also from a second device,
returns the first join. A restarted client is the same participant.

The replicator is its own process. By default it runs detached and join returns at once, which
suits agents whose shell ends after each command. It stops after session.ended or when you
leave. With --foreground, join stays, the replicator is its child and stops when join ends;
use it when the caller runs the command as a background task. Run join again to restart a
replicator that was killed; it resumes the file. There is at most one replicator per session
and participant. The runtime directory also holds the join state, the PID of the replicator
and its output. It is cleaned at logout for $XDG_RUNTIME_DIR.

With --mic or --wav FILE the client joins as captured: the ears transcribe your own speech
locally with Whisper and the gateway view shows each sentence. Only text leaves your computer.
Use a headset. A missing speech model is downloaded once; before join, the ears check the
models, the WAV file or the recorder, and if something is missing you do not join. "office help
ears" lists the flags and variables of the ears.

If you run join in a directory that "office sync init" set up, or in a worktree of it, the
replicator also keeps that clone up to date while it runs, like "office sync follow". Nothing
is printed for it; a problem (hub away, commits of your own in main, no access) never stops the
meeting and is written once to the output of the replicator. In any other directory join does
nothing extra.

Join also creates a read-only stage link (see "office help stage link") and prints it to stderr
as "stage: <url>", so stdout stays the path of the log. With --json the join object has it as
"stageUrl". If the link cannot be created, join still succeeds and says so on stderr.`,
		Examples: []string{`LOG=$(office meeting join weekly)   # prints the path of the local log`,
			"office meeting join weekly --mic --language english", "office meeting join weekly --foreground &"}},
	{Name: "meeting log", Group: groupMeeting, Summary: "print the path of the local log",
		Usage:    "meeting log",
		Details:  "Prints the path of the local log of the session that you joined on this machine. Without --json it prints the path only; --json adds the session, the participant and whether a replicator runs.",
		Examples: []string{`tail -f "$(office meeting log)" | grep --line-buffered '"type":"turn.end"'`}},
	{Name: "meeting view", Group: groupMeeting, Summary: "gateway view of your own utterances",
		Usage: "meeting view",
		Details: `Shows your own utterances with their status (final, sent, off_the_record) and whether you are
paused, as a filtered view of the local log. It prints one line per change and ends with the
session. Keys: p pauses or resumes, o marks the last sent sentence off the record, q quits.
With --json it prints one object per change, for example
{"utterance":{"eventId":12,"commandId":"...","text":"...","status":"sent"}}, {"paused":true}
and {"ended":true}.`,
		Examples: []string{"office meeting view"}},
	{Name: "meeting events", Group: groupMeeting, Summary: "read events after a cursor from the hub",
		Usage: "meeting events [--after N] [--limit N]",
		Details: `Reads the events after the event ID N (default 0) from the hub, in the line format of the local
log. Event IDs grow by one without gaps. Use it to fill a gap in a copy, or poll it when you
cannot follow a file. A sentence that is off the record comes as a placeholder: the same ID,
"offTheRecord": true, no text. --json prints {"events": [...], "more": bool}.`,
		Examples: []string{"office meeting events --session design-review --after 41 --limit 20",
			`office meeting events --session design-review | jq -c 'select(.type == "utterance.final")'`}},
	{Name: "meeting leave", Group: groupMeeting, Summary: "leave the meeting",
		Usage: "meeting leave", Details: "The replicator of your local log stops after you left.", Examples: []string{"office meeting leave"}},
	{Name: "meeting pause", Group: groupMeeting, Summary: "pause your transcription",
		Usage:    "meeting pause",
		Details:  "Tells the others that you are not captured for now. With --mic you can press p in the view instead.",
		Examples: []string{"office meeting pause"}},
	{Name: "meeting resume", Group: groupMeeting, Summary: "resume your transcription",
		Usage: "meeting resume", Details: "The counterpart of \"meeting pause\".", Examples: []string{"office meeting resume"}},
	{Name: "meeting end", Group: groupMeeting, Summary: "end the meeting for everyone",
		Usage: "meeting end [--release]",
		Details: `Stores the running minutes as the minutes of the session and writes session.ended. Use
"meeting leave" to leave alone. After the end, the session takes no new command, except the
release, an objection, a note about an artifact and, until the release, a mark off the record.

What the session created waits in its staging area for the release. If no human took part, the
hub releases it at once. Otherwise it releases it when the grace period ends (24 hours unless the
hub sets another length); the command prints when. Until then every human who took part can
release it ("office session release") or hold it ("office session hold").

--release ends the session and then releases it, as two commands; only a human can release. If
staged files quote sentences off the record, the release is refused as with "office session
release"; the session has ended all the same. --json prints {"event", "duplicate", "release": {"state", "deadline"}}, and with --release also
"released" as "office session release" prints it.`,
		Examples: []string{"office meeting end", "office meeting end --release"}},
	{Name: "say", Group: groupMeeting, Summary: "submit a typed utterance",
		Usage: "say <text>",
		Details: `Typed text does not end your turn. Run "office signal turn-end" when you are done, so that
agents that wait for the end of a turn wake up.`,
		Examples: []string{`office say "I can summarize that."`}},
	{Name: "utterance off-the-record", Group: groupMeeting, Summary: "mark one of your sentences off the record",
		Usage: "utterance off-the-record <event-id> | --last",
		Details: `You can mark only your own sentence; the hub checks that you are the speaker. The log gets a
new utterance.off_the_record line. Everyone who reads the session later gets a placeholder
instead of the text, and the stage hides it. Agents must not use such a sentence anywhere.`,
		Examples: []string{"office utterance off-the-record 42", "office utterance off-the-record --last"}},
	{Name: "signal turn-end", Group: groupMeeting, Summary: "tell the meeting that a turn has ended",
		Usage:    "signal turn-end [--participant P]",
		Details:  "With --mic the ears send turn.end for you. After typed text, send it yourself. --participant defaults to you.",
		Examples: []string{"office signal turn-end"}},
	{Name: "signal silence-long", Group: groupMeeting, Summary: "tell the meeting that it was silent for a long time",
		Usage:    "signal silence-long --duration D",
		Details:  "D is a duration such as 10s. The ears do not send it, because they hear only your own participant.",
		Examples: []string{"office signal silence-long --duration 30s"}},
	{Name: "signal addressed", Group: groupMeeting, Summary: "tell the meeting that a speaker addressed someone",
		Usage:    "signal addressed --participant P --utterance ID",
		Details:  "P is the participant whose name was mentioned; ID is the event ID of the utterance.",
		Examples: []string{"office signal addressed --participant bot --utterance 42"}},
	{Name: "hand raise", Group: groupMeeting, Summary: "raise your hand",
		Usage: "hand raise [--topic T]",
		Details: `The hand stays raised until you speak, raise it again (the new topic replaces the old one) or
leave. The stage shows the raised hands in the order they went up.`,
		Examples: []string{`office hand raise --topic "Budget"`}},
	{Name: "minutes update", Group: groupMeeting, Summary: "write the running minutes of the meeting",
		Usage: "minutes update <text> | --file F",
		Details: `The running minutes are the first artifact on the stage. "meeting end" stores the text of the
last update as the minutes of the session, with the participant who wrote it as the author.
--file - reads stdin.`,
		Examples: []string{"office minutes update --file minutes.md"}},

	// Speech and vocabulary.
	{Name: "models list", Group: groupEars, Summary: "list the speech models and the one that join would use",
		Usage: "models list [--language L] [--fast] [--model NAME|FILE]",
		Details: `Shows the catalogue of Whisper models by name, what is downloaded, and which model "meeting
join" would use for the same flags.

  small                  490 MB   fast, all languages; real time on a CPU
  large-v3-turbo         1.6 GB   accurate, all languages; needs a GPU for live use
  large-v3-turbo-german  1.6 GB   accurate, fine-tuned for German (primeline, Apache-2.0); needs a GPU

The ears choose the model in this order: 1. --model NAME|FILE; 2. --fast (small); 3.
OFFICE_WHISPER_MODEL, a name or a file; 4. the best model for --language: large-v3-turbo-german
for German, large-v3-turbo for every other language. If the build of the client has no GPU
support, the ears take small instead and say so on stderr. Only the macOS build for Apple
silicon (Metal) counts as a GPU build; OFFICE_WHISPER_GPU=1 or 0 overrides this, for example for
an own build with CUDA. Use a multilingual model, not a ".en" model: those only know English.

--language takes a code or an English name, such as de, german, en or english, or auto (Whisper
detects the language). The default is OFFICE_WHISPER_LANGUAGE, else de. Models are stored in
~/.local/share/agentic-office/models (or $XDG_DATA_HOME/agentic-office/models).

--json prints {"models": [...], "selected", "language", "note"}.`,
		Examples: []string{"office models list --language english", "office models list --fast"}},
	{Name: "models download", Group: groupEars, Summary: "download the speech model and the VAD model ahead of time",
		Usage: "models download [--language L] [--fast] [<name>]",
		Details: `The first "meeting join --mic" or "--wav" downloads a missing model of the catalogue and the
Silero VAD model into the model directory and checks their SHA-256. This command does the same
ahead of time, for example before the first meeting. Without a name it takes the model that
join would use. OFFICE_MODEL_URL sets a mirror: the files are then read from <mirror>/<file>,
for example <mirror>/ggml-large-v3-turbo-german.bin and <mirror>/ggml-silero-v6.2.0.bin. A model
file that you choose with a flag or a variable is never downloaded.`,
		Examples: []string{"office models download --language german", "office models download large-v3-turbo"}},
	{Name: "vocabulary extract", Group: groupEars, Summary: "write .office/vocabulary.txt from the documents of a project",
		Usage: "vocabulary extract [DIR] [--out FILE] [--max N] [--no-model] [--force]",
		Details: `Reads the Markdown and text files of DIR (default: the current directory; .md, .markdown,
.txt, .rst, .adoc; also in subdirectories, but not in hidden ones, node_modules or vendor) and
writes the terms that Whisper often misspells: names of persons, organisations and places
(found by a named-entity model for German, English and eight more languages), CamelCase words,
acronyms and words of headings. Code blocks, inline code, links and HTML are skipped. Each
occurrence scores: 3 for a name of the model, 2 for CamelCase or an acronym, 1 for a word of a
heading; the terms are ranked by score, then by first occurrence.

The model is distilbert-base-multilingual-cased-ner-hrl (AFL-3.0). The client runs it in plain
Go and downloads it on first use (about 540 MB, SHA-256 pinned, from the source or
OFFICE_MODEL_URL). It labels words and writes no text. It is slow: about 60 to 100 words per
second on 4 CPUs. --no-model uses the rules only: fast, no download. The command does not
replace a file that it did not write (it knows its files by the first line) unless you pass
--force. Check the file after the run: the most important terms belong at the top, because only
about 30 fit into the prompt of Whisper.

--json prints {"file", "documents", "model", "terms": [{"term", "score", "sources"}]}.`,
		Examples: []string{"office vocabulary extract docs/", "office vocabulary extract --no-model --max 50"}},

	// Stage, slides and documents.
	{Name: "stage focus", Group: groupStage, Summary: "show an artifact on the stage",
		Usage: "stage focus <artifact-id>",
		Details: `An artifact is what the stage shows large: the ID of a slide deck (slides/42), the path of a
document (documents/plan.md) or "minutes", the running minutes of the session. The minutes
are in focus until another artifact gets the focus. The hub does not check that the artifact
exists; the stage shows a note for an unknown ID.`,
		Examples: []string{"office stage focus slides/42", "office stage focus documents/plan.md", "office stage focus minutes"}},
	{Name: "portal link", Group: groupStage, Summary: "print a link that opens the portal in a browser",
		Usage: "portal link",
		Details: `Every user gets a link. It opens the portal at the tenant you work in (--tenant or
OFFICE_TENANT), or at its start page with --tenant "". The link works once and expires after a
few minutes. The portal then keeps you signed in for one hour in a cookie that scripts cannot
read. It shows what your key may see: your tenants, the members of a tenant to its tenant
admins, and the users and keys, tenants and system admins to system admins, who also register
and revoke keys there. Every page asks the hub with your rights.`,
		Examples: []string{"office portal link", `office portal link --tenant ""   # the start page`}},
	{Name: "stage link", Group: groupStage, Summary: "create a read-only link to the stage of a session",
		Usage: "stage link",
		Details: `Prints https://<hub>/stage/<session>?token=... for one session. The link is read only and
lives 24 hours (hub flag -stage-link-ttl).
The link also carries the address of the portal page of the session in your tenant (parameter
"portal"); the stage shows it as "Open in portal". It has no credential: the portal asks you
to sign in with "office portal link". Open it in a browser and share that window in your
meeting tool. The stage belongs to the tenant of the user who creates the link.`,
		Examples: []string{"office stage link --session weekly"}},
	{Name: "slides render", Group: groupStage, Summary: "render a slide deck and print its ID",
		Usage: "slides render <deck.json | deck.md>",
		Details: `The hub renders the deck into one self-contained HTML document for a screen share and prints
its ID, for example slides/42, which "stage focus" takes. The same deck always gives the same
output. "office help deck-format" describes the files. --session links the deck to a session
that you took part in: the deck then waits for the release of the session, and until then only
its participants see it. --json prints {"id", "mediaType", "staged", "duplicate"}.`,
		Examples: []string{`ID=$(office slides render deck.md --session weekly)`, `office stage focus "$ID"`}},
	{Name: "slides change", Group: groupStage, Summary: "replace a staged slide deck before the release",
		Usage: "slides change <id> <deck.json | deck.md> --session S",
		Details: `Replaces the title and the slides of a deck that waits for the release of its session.
--session must name that session, and only a participant of it may change it; you become the
author. A released deck never changes: render a new one. --json prints {"id", "mediaType",
"staged", "duplicate"}.`,
		Examples: []string{"office slides change slides/42 deck.md --session weekly"}},
	{Name: "slides remove", Group: groupStage, Summary: "remove a staged slide deck before the release",
		Usage: "slides remove <id> --session S",
		Details: `Removes a deck that waits for the release of its session, with the rules of "slides
change". Its ID is not used again. It also discards a deck that the release kept back because
its path was taken; from then on everyone reads the file at that path. --json prints {"id",
"duplicate"}.`,
		Examples: []string{"office slides remove slides/42 --session weekly"}},
	{Name: "slides get", Group: groupStage, Summary: "fetch a rendered deck",
		Usage:    "slides get <id> [--out FILE]",
		Details:  "Prints the rendered HTML, or writes it to FILE. --json prints {\"deck\": ...} with the input and the output (the content in base64).",
		Examples: []string{"office slides get slides/42 --out deck.html"}},
	{Name: "document create", Group: groupStage, Summary: "create a text document",
		Usage: "document create <path> <text> | --file F",
		Details: `A document is a Markdown file "documents/<name>.md" in the archive; its path is its identity.
The hub refuses other paths and content over 1 MiB, and it fails with already_exists when the
path is taken. --session links the document to a session that you took part in: the log then
gets artifact.created, and the document waits for the release of the session. Until then only
its participants read it; everyone else reads its stub. Prints the version of the document.
Never put a sentence that is off the record into a document; the release of the session is
refused while a staged file quotes one word for word.
Front matter that names "schema: office.document.v1" must follow the schema of documents, or
the hub refuses the document with invalid_argument. Every relative link that leads to no file
is printed as a "hint:" line on stderr; the document is written all the same.
--json prints {"path", "version", "staged", "duplicate", "hints"}.`,
		Examples: []string{"V=$(office document create documents/plan.md --file plan.md --session weekly)"}},
	{Name: "document change", Group: groupStage, Summary: "replace a text document",
		Usage: "document change <path> --base VERSION <text> | --file F",
		Details: `--base is the version that you read. If someone changed the document in the meantime, the
change fails with "aborted" and names the current version: read the document again and redo
your change. Of two changes with the same base, the first one wins. Your name is recorded as
the author of the change. --session links the change to a session (artifact.changed). A
document that waits for the release of its session changes only with --session of that
session, and only for a participant of it. A released document changes at once. The front
matter is checked and the hints are printed as with "document create".
--json prints {"path", "version", "staged", "duplicate", "hints"}.`,
		Examples: []string{`office document change documents/plan.md --base "$V" --file plan.md`}},
	{Name: "document remove", Group: groupStage, Summary: "remove a staged text document before the release",
		Usage: "document remove <path> --base VERSION --session S",
		Details: `Removes a document that waits for the release of its session. --session must name that
session, and only a participant of it may remove it. --base is the version that you read. It also
discards a document that the release kept back because its path was taken; from then on everyone
reads the file at that path. A released document is removed with Git, not here. --json prints
{"path", "duplicate"}.`,
		Examples: []string{`office document remove documents/plan.md --base "$V" --session weekly`}},
	{Name: "document get", Group: groupStage, Summary: "read a text document",
		Usage: "document get <path> [--out FILE]",
		Details: `Prints the content, or writes it to FILE. A document that waits for the release of its
session shows its content to its participants and the tenant admins; everyone else reads its
stub. --json prints the path, the version, whether it is staged, and the content.`,
		Examples: []string{"office document get documents/plan.md"}},
	{Name: "document list", Group: groupStage, Summary: "list the files of a directory of the archive",
		Usage: "document list [<dir>]",
		Details: `The default directory is documents. The archive read interface lists Markdown and YAML files
only. The session logs lie in a repository that no participant can read.`,
		Examples: []string{"office document list"}},

	// Artifacts in a local directory.
	{Name: "sync", Group: groupSync, Summary: "bring the directory up to date with the hub",
		Usage: "sync [<directory>]",
		Details: `Updates the main directory (default: the current directory) that "office sync init" set up:
it fetches the hub and moves main forward. It never merges. Files that you changed and did not
commit stay as they are.

If the hub rewrote the history of main, nothing of yours is on main, so main moves to the new
state of the hub. Files that you changed and did not commit stay, unless the move would
overwrite them: then the command stops and changes nothing.

The command also stops and changes nothing when
  - main has commits of its own: the main directory mirrors the hub, so move them to a branch
    in a worktree and push that branch;
  - an incoming change would overwrite a file that you changed and did not commit: commit or
    move the file, then run the command again.
It refuses a directory that is not set up and a linked worktree (it works on the main directory
only). Git gets its access token from the helper that "office sync init" wrote into the clone.

To keep the directory up to date while you work, use "office sync follow".

If the directory has the name of a command (init, follow, credential), write ./init, for
example "office sync ./init".

Git reads GIT_SSL_CAINFO before the certificate that "office sync init" wrote into the clone.
If the variable is set and names another file, the command warns; see "office help sync init".

--json prints {"directory", "updated", "from", "to", "rewritten"} (the commits of main; rewritten
is true when the hub had rewritten the history).`,
		Examples: []string{"office sync", "office sync ~/artifacts"}},
	{Name: "sync follow", Group: groupSync, Summary: "keep the directory up to date until you stop the command",
		Usage: "sync follow [<directory>] [--path P]...",
		Details: `Updates the main directory once (as "office sync" does), then follows the hub and updates the
directory after each change of an artifact, whether it came through the documents interface or
through a push. Stop it with Ctrl-C or SIGTERM.

It connects first, waits until the hub confirms that it is listening, and then updates, so no
change gets lost between the two. A change is announced without its content; the update always
takes everything that is new. When the connection ends or fails, the command waits (one second,
then longer, up to 30 seconds), connects again and updates before it follows. An update that
needs your action (own commits in main, a file that would be overwritten)
is reported on stderr and does not stop the command; the next change tries again.

--path limits the changes that start an update to artifacts under a directory or file of the
repository, for example notes or notes/plan.md. You can repeat it. An update still brings
everything.

With --json, every update prints {"directory", "updated", "from", "to", "rewritten"} on stdout, and messages
on stderr are {"progress": text}.`,
		Examples: []string{"office sync follow", "office sync follow ~/artifacts --path documents --path notes/plan.md"}},
	{Name: "sync init", Group: groupSync, Summary: "set up a directory with the artifacts of the tenant",
		Usage: "sync init [<directory>]",
		Details: `Clones the artifact repository of the tenant from the hub into the directory (default: the
current directory) with the Git command line tool, which must be in PATH. The hub address and
the tenant come from the active hub profile or configuration ("office help config"); you must
be a member of the tenant.

The directory must be empty, new, or a clone of this tenant that you set up before. Any other
directory is refused and nothing is changed. Run the command again on a set-up directory to
repair its configuration; it does not touch your files.

The command writes into the configuration of the clone only, never into your global Git
configuration: you as the author (your display name and <participant ID>@office.invalid), a
helper that gets a fresh access token from the hub each time Git needs one (so no token is
stored and none is in the address), the certificate that the hub profile trusts, and a guard
that refuses a commit in this main directory. The main directory mirrors the hub. To change
files, work in a worktree: git worktree add ../my-change -b my-change.

If the environment sets GIT_SSL_CAINFO to another file than the certificate of the clone, plain
Git uses that file and not the certificate of the hub, and the command warns. Unset the variable
or point it at a bundle that also holds the certificate of the hub. Do not turn off the check.

If the directory has the name of a command (init, follow, credential), write ./init, for
example "office sync init ./init".

--json prints {"directory", "repository", "cloned", "author"}.`,
		Examples: []string{"office sync init ~/artifacts", "office sync init --tenant acme"}},
	{Name: "sync credential", Group: groupSync, Summary: "Git credential helper (Git runs it, not you)",
		Usage: "sync credential get|store|erase",
		Details: `The program that "office sync init" writes into the configuration of the clone as the Git
credential helper. For "get", Git sends the host on its input and the command prints the user
name and a fresh access token of your key, but only for the hub of this connection. "store" and
"erase" do nothing. You do not need to run it yourself.`,
		Examples: []string{"printf 'protocol=https\nhost=hub.example.org:8740\n\n' | office sync credential get --hub https://hub.example.org:8740 --tenant acme"}},

	// Decisions, notes, minutes and tasks.
	{Name: "decision record", Group: groupRecords, Summary: "record a decision",
		Usage: "decision record <text> | --file F [--session S]",
		Details: `The decision gets the author and kind from your key. It is linked to a session only with an
explicit --session, and only a participant of the session may name it. A decision of a session
that is not released waits in the staging area of the session until the release; until then every
participant may change or remove it (decision change, decision remove). Never put a sentence that
is off the record into a record; the hub does not check this. --json prints {"decision": ...,
"duplicate": ...}.`,
		Examples: []string{`office decision record "We ship on Friday."`, "office decision record --session weekly --file decision.md"}},
	{Name: "decision list", Group: groupRecords, Summary: "list recorded decisions",
		Usage: "decision list [--after N] [--limit N] [--session S]",
		Details: `Oldest first, after the record ID N, at most --limit records (default 100, at most 1000). To
read the next page, pass the ID of the last record. A staged record shows "(staged)" to the
participants of its session and to tenant admins; everyone else sees only its ID and that it waits
for the release of its session (in --json: "stub": true). --json prints {"decisions": [...]}.`,
		Examples: []string{"office decision list --after 10 --limit 20"}},
	{Name: "note add", Group: groupRecords, Summary: "add a note",
		Usage: "note add <text> | --file F [--tag T]... [--session S]",
		Details: `The text is Markdown. The hub stores the note as a Markdown file with front matter
(notes/NNNNNN.md). --tag adds a tag; repeat it for more tags. Like a decision, a note is linked to
a session only with an explicit --session, and it waits for the release of that session.
--json prints {"note": ..., "duplicate": ...}.`,
		Examples: []string{`office note add "Ask the bank about the loan."`, `office note add --tag budget --tag bank --file note.md`}},
	{Name: "note list", Group: groupRecords, Summary: "list notes",
		Usage:    "note list [--after N] [--limit N] [--session S]",
		Details:  "Paging as for \"decision list\". --json prints {\"notes\": [...]}.",
		Examples: []string{"office note list --session weekly"}},
	{Name: "minutes show", Group: groupRecords, Summary: "show the minutes stored when a session ended",
		Usage: "minutes show [<session>]",
		Details: `Prints the text of the newest stored minutes of the session (default: --session), or fails
with not_found. Until the release of the session, only its participants and the tenant admins
read its minutes. --json prints {"minutes": ...}.`,
		Examples: []string{"office minutes show weekly"}},
	{Name: "minutes list", Group: groupRecords, Summary: "list stored minutes",
		Usage:    "minutes list [--after N] [--limit N] [--session S]",
		Details:  "Paging as for \"decision list\". --json prints {\"minutes\": [...]}.",
		Examples: []string{"office minutes list"}},
	{Name: "decision change", Group: groupRecords, Summary: "change a staged decision before the release",
		Usage: "decision change <id> <text> | --file F --session S",
		Details: `Replaces the text of a decision that waits for the release of its session. --session must
name that session, and only a participant of it may change it; you become the author. A released
decision never changes: record a new one that refers to it. --json prints {"decision": ...,
"duplicate": ...}.`,
		Examples: []string{`office decision change 4 --session weekly "We ship on Monday."`}},
	{Name: "decision remove", Group: groupRecords, Summary: "remove a staged decision before the release",
		Usage: "decision remove <id> --session S",
		Details: `Removes a decision that waits for the release of its session, with the rules of "decision
change". Its ID is not used again. It also discards a decision that the release kept back because
its path was taken; from then on everyone reads the file at that path. --json prints {"id": ...,
"duplicate": ...}.`,
		Examples: []string{"office decision remove 4 --session weekly"}},
	{Name: "note change", Group: groupRecords, Summary: "change a staged note before the release",
		Usage: "note change <id> <text> | --file F [--tag T]... --session S",
		Details: `Replaces the text and the tags of a note that waits for the release of its session, with
the rules of "decision change". --json prints {"note": ..., "duplicate": ...}.`,
		Examples: []string{`office note change 7 --session weekly --tag bank "Ask the bank about the loan in May."`}},
	{Name: "note remove", Group: groupRecords, Summary: "remove a staged note before the release",
		Usage:    "note remove <id> --session S",
		Details:  `Removes a note that waits for the release of its session, with the rules of "decision remove".`,
		Examples: []string{"office note remove 7 --session weekly"}},
	{Name: "minutes change", Group: groupRecords, Summary: "change the staged minutes before the release",
		Usage: "minutes change <id> <text> | --file F --session S",
		Details: `Replaces the text of the minutes that the hub stored when the session ended, while they
wait for the release, with the rules of "decision change". Take the ID from "minutes show --json".
--json prints {"minutes": ..., "duplicate": ...}.`,
		Examples: []string{"office minutes change 3 --session weekly --file minutes.md"}},
	{Name: "minutes remove", Group: groupRecords, Summary: "remove the staged minutes before the release",
		Usage:    "minutes remove <id> --session S",
		Details:  `Removes the minutes of a session that wait for the release, with the rules of "decision remove".`,
		Examples: []string{"office minutes remove 3 --session weekly"}},
	{Name: "task create", Group: groupRecords, Summary: "create a task",
		Usage: "task create <title> [--description D | --description-file F] [--assignee P] [--status S] [--session S]",
		Details: `For now every task is a local draft of the hub. Its ref, for example local:42, is opaque: keep
it and pass it back. Statuses are open (the default), in_progress (or in-progress), done and
cancelled. The hub checks neither the assignee nor the session. Any participant may read and
change every task. --json prints {"task": ..., "duplicate": ...}.`,
		Examples: []string{`office task create "Write the release notes" --assignee bot --session weekly`}},
	{Name: "task get", Group: groupRecords, Summary: "show one task",
		Usage: "task get <ref>", Details: "--json prints {\"task\": ...}.", Examples: []string{"office task get local:1"}},
	{Name: "task list", Group: groupRecords, Summary: "list tasks",
		Usage: "task list [--status S[,S]] [--assignee P] [--session S] [--limit N] [--page-token T]",
		Details: `Oldest first, at most --limit tasks (default 100, at most 1000). If more tasks match, the
token of the next page is printed on stderr (--json: "nextPageToken"); pass it as --page-token.
--json prints {"tasks": [...], "nextPageToken": ...}.`,
		Examples: []string{"office task list --status open,in_progress --assignee bot"}},
	{Name: "task update", Group: groupRecords, Summary: "change a task",
		Usage: "task update <ref> [--title T] [--description D | --description-file F] [--assignee P] [--status S] [--session S]",
		Details: `Only the fields that you set change. An empty assignee or session removes it. An update that
changes nothing makes no new revision. --json prints {"task": ..., "duplicate": ...}.`,
		Examples: []string{"office task update local:1 --status done", `office task update local:1 --assignee "" --session ""`}},

	{Name: "session list", Group: groupRecords, Summary: "list the sessions that you may see",
		Usage: "session list",
		Details: `Lists the sessions of the tenant in which you took part or that you imported, the newest
first; a tenant admin sees every session of the tenant. Each line: the session (marked when it was
imported), the start, the end or "running" (UTC), and the participants. --json prints
{"sessions": [{"sessionId", "startedAt", "endedAt", "imported", "participants": [...]}]}.`,
		Examples: []string{"office session list", "office session list --json"}},

	{Name: "session release", Group: groupRecords, Summary: "release what an ended session created",
		Usage: "session release [<session>] [--accept-quotes TOKEN]",
		Details: `Moves what the session staged (minutes, notes, decisions, documents, slides) from its
staging area into the artifact repository of the tenant, in one commit with you as the author.
Every member sees it then. Only a human who took part in the session may release it, also after
leaving; the session must have ended. A release lifts an objection. Without <session>, the session
is --session or the only session you joined on this computer.

Before the release, the hub searches the staged files for sentences that are off the record, word
for word. If it finds one, it refuses: the command prints each place (file, line, event of the
sentence, never the sentence), a token, and exits with 1. Correct the files and release again, or
release anyway with --accept-quotes TOKEN. The hub searches again and releases only if it finds
exactly the same quotes; otherwise it refuses with a new token. The hub changes no content.

--json prints {"event", "duplicate", "quotes": [{"path", "line", "utteranceEventId"}]}, with the
quotes that the token accepted. A refusal because of quotes is an error: {"error": {"code":
"failed_precondition", "message", "quotes", "quoteToken"}}.`,
		Examples: []string{"office session release weekly", "office session release weekly --accept-quotes 3f9c…", "office session release --json"}},
	{Name: "session hold", Group: groupRecords, Summary: "object to the release of an ended session",
		Usage: "session hold [<session>]",
		Details: `Holds the release: when the grace period ends, the hub does not release the session. A
human who took part releases it later with "office session release". Use it when a staged file
needs a correction first, for example because it repeats a sentence that is off the record. Only a
human who took part may object, once, before the release.`,
		Examples: []string{"office session hold weekly"}},

	// Import.
	{Name: "session import", Group: groupImport, Summary: "import a conversation as a marked session, or extend one",
		Usage: "session import <conversation.json | -> [--session S]",
		Details: `Imports a whole conversation from another tool (a chat export, a meeting transcript, an agent
conversation) as a new session and prints its ID. Without --session it creates the ID
(import-<uuid>); "-" reads stdin. The client sends the conversation in pages of 500 utterances.
Every error in the file is a usage error (exit code 2) and nothing is imported. The log starts
with session.imported, then participant.joined (imported: true) for each speaker, then one
utterance.final per message, and ends with import.completed. The signed tree heads prove
which participant imported the log and that it has not changed since; they do not prove that the
speakers said it. After the import the session is a normal session: join, write minutes, end it.

With --session of an imported session that exists, the import extends it: the conversation went
on, or an earlier import stopped halfway. The file must start with every utterance that the
session holds, unchanged and in the same order; the import adds the new participants and the
utterances after them, and import.completed with the new total. Only the participant who
imported the session extends it, and only before the session ends. A file that adds nothing
imports nothing. A file that does not start with the utterances of the session is a usage error.

"office help import-format" describes the file. --json prints {"sessionId", "pages",
"firstEventId", "lastEventId", "participants", "utterances", "extended", "previousUtterances",
"treeHead"}; participants and utterances count what the import added.`,
		Examples: []string{"office session import design.json --session design-review"}},

	// Agents.
	{Name: "mcp", Group: groupAgents, Summary: "run the MCP server on stdin and stdout",
		Usage: "mcp [--session S]",
		Details: `An MCP client starts this command. It offers the features of the CLI as tools for agents that
use MCP instead of a shell. It takes the same flags and OFFICE_* variables as the CLI;
--session sets the default session of the tools. The key must exist and be registered first.
Tool names use "_" where the CLI uses a space or a dot, for example meeting_join, utterance_say
and task_create; the tools return the same JSON as "--json". Not offered: key and admin
commands, the gateway view and the ears. meeting_join runs the log replicator inside the MCP
process: it stops when the client closes the connection, when you leave or after session.ended;
call meeting_join again to restart it. The tool descriptions explain the log file and the
event types to the agent.`,
		Examples: []string{`claude mcp add office -e OFFICE_KEY="$HOME/.config/agentic-office/agent/key.pem" -- office mcp`}},
	{Name: "team-agent create", Group: groupAgents, Summary: "create a team agent of the tenant",
		Usage: "team-agent create <agent> [--name NAME]",
		Details: `Only a tenant admin of the tenant may call it. A team agent belongs to the tenant, not to a
person, and has no key: every member of the tenant can start it with "office agent run
--team-agent" (see "office help agent run"). The agent ID is a participant ID, so it is
unique in the whole hub and never used again. The definition of the agent is a file of the
tenant, agents/<agent>.yaml, that members change with Git (see "office help agent run").
--name is the display name (default: the agent ID).`,
		Examples: []string{`office team-agent create minutes --name "Minutes keeper" --tenant acme`}},
	{Name: "team-agent list", Group: groupAgents, Summary: "list the team agents of the tenant",
		Usage:    "team-agent list",
		Details:  "Every member of the tenant may call it. A removed team agent shows as removed.",
		Examples: []string{"office team-agent list --tenant acme"}},
	{Name: "team-agent remove", Group: groupAgents, Summary: "end a team agent of the tenant",
		Usage: "team-agent remove <agent>",
		Details: `Only a tenant admin of the tenant may call it. The tokens of the agent stop working at once.
The record stays and the agent ID is never used again.`,
		Examples: []string{"office team-agent remove minutes --tenant acme"}},
	{Name: "agent run", Group: groupAgents, Summary: "keep an agent in a meeting and wake it when it is needed",
		Usage: "agent run --session S [--role T | --role-file F | --team-agent AGENT [--confirm VERSION]] [--allow PATTERN]... [--cwd DIR] [wake flags] -- <harness command>",
		Details: `Claude Code, opencode and Kiro work in turns: they finish a turn and wait. The runner starts the
harness (the program after "--") as an Agent Client Protocol (ACP) agent, hands it the office
tools of this meeting, tells it its role and wakes it. It joins the session with this key
(use the key of the agent: OFFICE_KEY or --key), stops after session.ended once the agent
finished its last work, after the agent's own leave, on SIGINT or SIGTERM, or when the harness
stops (an error). It logs to stderr, each line starting with "office agent:".

Every flag and the harness command can also come from the configuration file (keys agent.* and
agent.wake.*); with a project file, "office agent run" alone is enough. "office help config"
lists the keys.

--wake chooses when the runner wakes the agent. turn-end (the default) wakes it at each change
of speaker. llm asks a forwarding judge that you bring after each utterance of another
participant: --decide-cmd CMD (a shell command that reads the request as JSON on stdin and
writes the answer as JSON on stdout), or --decide-url URL --decide-model M (the base URL of an
OpenAI-compatible API that ends in /v1; the runner appends /chat/completions). decision-model
asks a decision model of the System One API: --decide-url is the full URL of the endpoint,
nothing is appended; a probability at or above --decide-threshold (default 0.5) counts as yes.
A key in OFFICE_DECIDE_KEY (or the variable that agent.wake.key-env names) goes to the endpoint
as a bearer token and is never logged. The runner refuses model flags with turn-end. When the
model fails (error, time-out after --decide-timeout, an answer outside the contract), the
runner falls back to turn-end.

Permission: the runner offers the harness no file system and no terminal. By default it allows
a call of an office tool unless its kind is execute, edit, delete or move, and rejects every
other call. With --allow it allows exactly the calls whose title matches one of the patterns
(* is any text, case is ignored; --allow '*' allows everything).

Team agent: with --team-agent AGENT the runner starts the team agent of the tenant (create it with
"office team-agent create"). You are its agent launcher: you prove your own key, and the hub
gives the runner a token as the agent. The agent joins and writes under its own name; its
commits and records name you and the version of its definition. The harness gets only the token
of the agent, never your key, and every start of the same agent in the same session by another
member is refused ("already joined"). The role, the extra tools and the wake strategy come from
the definition, a YAML file of the tenant at agents/<agent>.yaml with the schema office.agent.v1:

  schema: office.agent.v1
  role: You keep the minutes of the weekly.
  allow: [fetch]            # patterns of tool titles to allow besides the office tools; optional
  wake:                     # optional
    strategy: turn-end      # turn-end, llm or decision-model
    threshold: 0.5          # for decision-model

Members change the file with Git in their clone of the tenant ("office sync"); the hub checks the
schema and refuses a push of an agent that changes it. The definition holds no command, no
working directory, no address and no key: the harness, --cwd, --decide-cmd, --decide-url and
the key of a model stay on your computer. The first time, and whenever the definition has
changed since you confirmed it, the runner shows it and asks "Run it? [y/N]". --confirm VERSION
confirms that version without a question (the runner names it when it asks). Give no --role and
no --role-file with --team-agent. The tools of the definition are allowed in addition to the rule
that --allow or the default gives.

Harness commands: opencode acp; kiro-cli acp; npx -y @agentclientprotocol/claude-agent-acp (the
ACP adapter of Claude Code; needs Node.js). Log in with the harness itself first: the runner does
not authenticate a harness.`,
		Examples: []string{`office agent run --session weekly --role "You keep the minutes." -- opencode acp`,
			`office agent run --session weekly --team-agent minutes --tenant acme -- opencode acp`,
			"office agent run --session weekly --role-file minutes.md --wake llm --decide-url http://127.0.0.1:11434/v1 --decide-model qwen3:4b -- kiro-cli acp"}},

	// Other.
	{Name: "replicator run", Group: groupInternal, Summary: "run the log replicator (started by meeting join)",
		Usage: "replicator run --session S --participant P --log FILE [--pid-file FILE] [--parent-pid N] [--ready-fd N]",
		Details: `The process that "meeting join" starts to copy the session stream into the local log. It holds
a lock on the log file, so there is at most one per session and participant. After a crash it
cuts an incomplete last line and resumes after the last complete line; after a network drop it
reconnects with a backoff. You do not need to run it yourself.`,
		Examples: []string{"office replicator run --session weekly --participant ada --log ./ada.jsonl"}},
	{Name: "version", Group: groupInternal, Summary: "print the version",
		Usage: "version", Details: "Prints the version of this build.", Examples: []string{"office version"}},
	{Name: "help", Group: groupInternal, Summary: "print this help, the help of a command or a topic",
		Usage:    "help [<command> | <topic>]",
		Details:  "Without an argument it lists all commands. \"office help <command>\" is the same as \"office <command> --help\".",
		Examples: []string{"office help", "office help meeting join", "office help config"}},
})

func buildHelp(hs []Help) map[string]Help {
	m := make(map[string]Help, len(hs))
	for _, h := range hs {
		m[h.Name] = h
	}
	return m
}
