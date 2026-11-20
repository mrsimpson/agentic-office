// Package commands is the contract of the office CLI (if-cli): the commands with their flags, the
// exit codes and the form of the --json output. The office CLI (client/cli) implements it.
//
// Conventions:
//
//   - Flags may come before or after the positional arguments. Every command takes the common
//     flags --json, --session, --hub, --hub-profile, --ca-cert, --tenant, --key and
//     --runtime-dir.
//   - With --json, stdout carries exactly one JSON object, also on failure (ErrorJSON). Events
//     have the form of their line in the local log.
//   - Exit codes: ExitOK, ExitError and ExitUsage.
//   - Every command explains itself (Help): "office <command> --help" and "office help <command>"
//     print its purpose, synopsis, flags with defaults and variables, and an example. "office
//     help" lists all commands (Overview). "office help <topic>" prints a topic that belongs to
//     no single command: configuration, the --json output, the import format, the deck files and
//     the ears (Topic). Names lists the commands that have help.
package commands

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

// ErrorJSON is the --json output of a failed command: {"error":{"code":…,"message":…}}. detail adds
// fields to the error object, for example "quotes" and "quoteToken" of a release refused because
// of quotes.
func ErrorJSON(code, message string, detail map[string]any) map[string]any {
	e := map[string]any{"code": code, "message": message}
	for k, v := range detail {
		e[k] = v
	}
	return map[string]any{"error": e}
}
