// Package commands is the contract of the office CLI (if-cli): the commands with their flags, the
// exit codes and the form of the --json output. The office CLI (client/cli) implements it.
//
// Conventions:
//
//   - Flags may come before or after the positional arguments. Every command takes the common
//     flags --json, --session, --hub, --ca-cert, --key and --runtime-dir.
//   - With --json, stdout carries exactly one JSON object, also on failure (ErrorJSON). Events
//     have the form of their line in the local log.
//   - Exit codes: ExitOK, ExitError and ExitUsage.
package commands

// Usage lists the commands, their flags and the common flags. "office help" prints it.
const Usage = `usage: office <command> [flags]

Keys and identity:
  key create                         create a key pair (if there is none) and print the public key
  key show                           print the public key and key ID
  whoami                             authenticate and print the participant ID
  admin key register <participant> <public-key> [--name N] [--kind human|agent]
  admin key revoke <key-id>

Meeting:
  meeting join <session> [--name N] [--foreground] [--mic | --wav FILE]
               [--language L] [--fast] [--model NAME|FILE] [--vocabulary FILE] [--context]
                                     join, start the log replicator, print the log path; with
                                     --mic or --wav, a missing model is downloaded once
  models list [--language L] [--fast] [--model NAME|FILE]
                                     the speech models, what is downloaded, and which one join uses
  models download [--language L] [--fast] [<name>]
                                     download the chosen speech model and the VAD model ahead of time
  vocabulary extract [DIR] [--out FILE] [--max N] [--no-model] [--force]
                                     find names and terms in the documents of DIR (NER model and
                                     heuristics) and write .office/vocabulary.txt for Whisper
  meeting log                        print the path of the local log
  meeting view                       gateway view: own utterances; keys p pause, o off the record, q quit
  meeting events [--after N] [--limit N]  events after a cursor, read from the server (for gaps or without a file system)
  meeting leave | pause | resume | end
  say <text>                         submit a typed utterance
  utterance off-the-record <event-id> | --last
  signal turn-end [--participant P]
  signal silence-long --duration D
  signal addressed --participant P --utterance ID
  hand raise [--topic T]
  minutes update <text> | --file F   (--file - reads stdin)
  stage show <artifact-id>
  stage link                         create a read-only link to the stage

Import (dec-session-import; the format is in README.md):
  session import <conversation.json | ->  import a conversation as a new, marked session and
                                     print its ID (--session S chooses the ID; - reads stdin)

Slides (the ID, such as slides/42, is what stage show takes):
  slides render <deck.json | deck.md>  render a deck and print its ID
  slides get <id> [--out FILE]       the rendered deck (--json: input and output)

Records (never put a sentence that is off the record into them):
  decision record <text> | --file F  record a decision (--session links it to a session)
  decision list [--after N] [--limit N]
  note add <text> | --file F         add a note (--session links it to a session)
  note list [--after N] [--limit N]
  minutes show [<session>]           the minutes stored when the session ended
  minutes list [--after N] [--limit N]
  (the list commands take --session to show only the records of one session)

Tasks (local drafts in the hub for now; a ref such as local:42 is opaque):
  task create <title> [--description D | --description-file F] [--assignee P] [--status S]
                                     create a task (--session links it to a session)
  task get <ref>
  task list [--status S[,S]] [--assignee P] [--page-token T] [--limit N]  (--session filters)
  task update <ref> [--title T] [--description D | --description-file F] [--assignee P] [--status S]
                                     change a task (--session S links it, --session "" removes the link)
  (statuses: open, in_progress, done, cancelled)

MCP:
  mcp                                run the MCP server on stdin/stdout (for an MCP client; see README.md)

Agent runner (keeps an agent in the meeting over the Agent Client Protocol; see README.md):
  agent run --session S [--role T | --role-file F] [--allow PATTERN]... [--cwd DIR] -- <harness command>
                                     join S with this key, start the harness (for example
                                     "opencode acp") and wake it when the meeting may concern it

Other:
  version                            print the version
  help                               print this help

Flags of every command:
  --json            print one JSON object on stdout (env OFFICE_JSON=1)
  --session S       the session; default: the only session joined from this machine (env OFFICE_SESSION)
  --hub URL         office hub (env OFFICE_HUB, default https://127.0.0.1:8740)
  --ca-cert FILE    trust this server certificate (env OFFICE_CA_CERT)
  --key FILE        private key of this actor (env OFFICE_KEY)
  --runtime-dir D   directory for local logs and state (env OFFICE_RUNTIME_DIR)

Exit codes: 0 success, 1 error, 2 usage error. See README.md for the --json format.
`

// Exit codes of the office CLI.
const (
	// ExitOK: the command succeeded.
	ExitOK = 0
	// ExitError: the command failed.
	ExitError = 1
	// ExitUsage: the call was wrong (an unknown command, a wrong flag or argument).
	ExitUsage = 2
)

// Error codes in the --json output of a failed command. An error of the hub has its Connect code
// instead, for example "not_found" or "permission_denied".
const (
	CodeUsage         = "usage"
	CodeNoKey         = "no_key"
	CodeNoSession     = "no_session"
	CodeUnimplemented = "unimplemented"
	CodeError         = "error"
)

// ErrorJSON is the --json output of a failed command: {"error":{"code":…,"message":…}}.
func ErrorJSON(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}
