package agentrunner

import (
	"fmt"
	"strings"
)

// firstPrompt tells the agent its role and how the meeting works.
func (r *runner) firstPrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "You take part in the meeting %q of the agentic office as the participant %q, an agent.\n\n",
		r.o.Session, r.self)
	b.WriteString("Your role:\n")
	if role := strings.TrimSpace(r.o.Role); role != "" {
		b.WriteString(role)
	} else {
		b.WriteString("No role was given. Listen, and answer when someone addresses you.")
	}
	b.WriteString("\n\nHow this works:\n")
	b.WriteString("- You have joined the session already. The tools of the MCP server \"office\" act as you in this session; you do not need to call meeting_join.\n")
	b.WriteString("- I am the agent runner. When something in the meeting may concern you, I send you what the others said since my last message: who spoke, whether that participant is a human or an agent, and what they said.\n")
	b.WriteString("- Decide yourself whether someone addressed you or whether your role asks you to contribute now. Do not interrupt a speaker: raise your hand or write instead. If nothing is for you, do nothing and end your turn.\n")
	b.WriteString("- To speak in the meeting, call the tool utterance_say. Your reply in this chat reaches nobody.\n")
	b.WriteString("- Never use or repeat a sentence that is off the record.\n")
	fmt.Fprintf(&b, "- The local log of the session is the file %s (one JSON line per event). Read it if you need earlier events.\n",
		r.m.joined.Paths.Log)
	b.WriteString("\nIf your role asks you to do something at the start of the meeting, do it now. Otherwise end your turn.")
	return b.String()
}
