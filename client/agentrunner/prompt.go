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

// writeBatch writes the utterances of a batch, one per line, and the notices of sentences that
// went off the record.
func (r *runner) writeBatch(b *strings.Builder, bt batch) {
	if len(bt.utterances) == 0 {
		b.WriteString("Nobody said anything new.\n")
	}
	for _, u := range bt.utterances {
		p := r.who(u.pid)
		fmt.Fprintf(b, "[%d] %s (%s): %s\n", u.id, p.name, p.kind, strings.Join(strings.Fields(u.text), " "))
	}
	if len(bt.offNotices) > 0 {
		ids := make([]string, len(bt.offNotices))
		for i, id := range bt.offNotices {
			ids[i] = fmt.Sprintf("[%d]", id)
		}
		fmt.Fprintf(b, "\nThe speaker marked these sentences off the record afterwards: %s. Do not use or repeat them, and keep them out of minutes, decisions, notes and tasks.\n",
			strings.Join(ids, ", "))
	}
}

// defaultTopicTask is the task at the end of a block of topics when the user gave none.
const defaultTopicTask = "If your role asks for it, update the minutes and record the decisions and tasks of that block now."

// wakePrompt is the prompt that wakes the agent (dec-wake-check-per-utterance). It says whether
// the speaker has ended their turn and what the model of the wake strategy found: whom the
// speaker addressed and whether a block of topics has ended.
func (r *runner) wakePrompt(bt batch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "New in the meeting %q since my last message (event ID, speaker, human or agent, text):\n\n", r.o.Session)
	r.writeBatch(&b, bt)
	if bt.turnBy != "" {
		p := r.who(bt.turnBy)
		fmt.Fprintf(&b, "\nThe turn of %s (%s) has ended.\n", p.name, p.kind)
	} else if len(bt.utterances) > 0 {
		b.WriteString("\nThe speaker may still be speaking. Do not interrupt: raise your hand or write instead, unless the speaker asked you to answer now.\n")
	}
	if bt.topicClosed {
		task := strings.TrimSpace(r.o.TopicTask)
		if task == "" {
			task = defaultTopicTask
		}
		fmt.Fprintf(&b, "\nA block of topics has ended. %s\n", task)
	}
	if bt.addressed != "" {
		fmt.Fprintf(&b, "\nThe model of the runner thinks that the speaker addressed: %s.\n", bt.addressed)
	}
	b.WriteString("\nDecide yourself whether someone addressed you or whether your role asks you to contribute now. If not, do nothing and end your turn. To speak, call utterance_say.")
	return b.String()
}

// finalPrompt is the last prompt after session.ended.
func (r *runner) finalPrompt(bt batch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The meeting %q has ended.\n\n", r.o.Session)
	if !bt.empty() {
		b.WriteString("New since my last message:\n\n")
		r.writeBatch(&b, bt)
		b.WriteString("\n")
	}
	b.WriteString("If your role has work for the end of the meeting, for example the minutes, decisions or tasks, do it now. Do not speak in the meeting any more: a new utterance would open the session again. Then end your turn. This is my last message; I stop after it.")
	return b.String()
}
