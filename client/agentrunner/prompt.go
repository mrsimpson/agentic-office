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
	b.WriteString("- To speak in the meeting, call the tool utterance_say. This is the only way to answer. Text that you write in this chat reaches nobody in the meeting; only the log of the runner on this computer shows it.\n")
	b.WriteString("- Decide yourself whether someone addressed you or whether your role asks you to contribute now. Do not interrupt a speaker: raise your hand or write instead. If nothing is for you, end your turn without text.\n")
	b.WriteString("- If you take responsibility for an artifact in this meeting, for example the minutes or bringing the results into the wiki, say so in the meeting as soon as you take it on, so that nobody else does it too. If someone already said so, leave it to them.\n")
	b.WriteString("- When you read or write the memory of the team (notes, decisions, minutes and the documents of its wiki), follow the skill office-memory if you have it. Never change minutes, decisions or notes once they are released.\n")
	fmt.Fprintf(&b, "- Everything you write that comes from this meeting (notes, decisions, documents, slides) must name the session %q. Then it waits until a human releases the meeting.\n", r.o.Session)
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
	b.WriteString("\nDecide yourself whether someone addressed you or whether your role asks you to contribute now. If not, end your turn without text. To answer, call utterance_say: text in this chat reaches nobody in the meeting.")
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

// maxQuote is the most of an unspoken reply that the runner quotes back to the agent, in bytes.
const maxQuote = 2000

// unspokenPrompt asks the agent once whether a reply that it wrote only in its chat was meant
// for the meeting (issue 170).
func (r *runner) unspokenPrompt(reply string) string {
	if len(reply) > maxQuote {
		reply = strings.ToValidUTF8(reply[:maxQuote], "") + " …"
	}
	var b strings.Builder
	b.WriteString("You ended your turn with this reply in your chat, and you did not call utterance_say. Nobody in the meeting heard it; only the log of the runner on this computer shows it:\n\n")
	for _, line := range strings.Split(reply, "\n") {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	b.WriteString("\nWas it meant for the meeting? Then say it now with utterance_say. If the speaker is still speaking, raise your hand with hand_raise instead. If it was only a note for yourself, end your turn without text.")
	return b.String()
}
