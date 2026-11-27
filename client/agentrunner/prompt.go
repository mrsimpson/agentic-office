package agentrunner

import (
	"fmt"
	"strings"
	"time"
)

// overlapMarker ends the line of an utterance that carries the hint of the ears that another voice
// may be in it (dec-overlap-hint). The first prompt explains it.
const overlapMarker = "[another voice may be in this sentence]"

// firstPrompt tells the agent its role and how the meeting works. It holds every instruction of
// the runner: the prompts that wake the agent carry data only. restarted: the runner started the
// harness again after a failure, so the agent remembers nothing of its earlier turns.
func (r *runner) firstPrompt(restarted bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You take part in the meeting %q of the agentic office as the participant %q, an agent.\n\n",
		r.o.Session, r.self)
	b.WriteString("Your role:\n")
	if role := strings.TrimSpace(r.o.Role); role != "" {
		b.WriteString(role)
	} else {
		b.WriteString("No role was given. Listen, and answer when someone addresses you.")
	}
	b.WriteString("\n\nHow the meeting works:\n")
	b.WriteString("- You have joined the session already. The tools of the MCP server \"office\" act as you in this session; you do not need to call meeting_join.\n")
	b.WriteString("- I am the agent runner. When something in the meeting may concern you, I send you what the others said since my last message: the event ID, the local time, who spoke, whether that participant is a human or an agent, and what they said. I also tell you whether the speaker's turn has ended")
	if r.modelWakes() {
		b.WriteString(", whom a model estimates that the speaker addressed, and when it estimates that a block of topics has ended")
	}
	b.WriteString(".\n")
	b.WriteString("- Most of it was spoken and transcribed. Names and terms may be written wrongly. Read them as the speaker most likely meant them, and never correct anyone's wording. If a word that matters is unclear, ask back in one short question.\n")
	fmt.Fprintf(&b, "- Some sentences end with the note %s. The ears heard another voice, or one they could not assign, at the same time or right next to it. Such a sentence may be incomplete, or may hold words of someone else. Do not act on a doubtful sentence before you have asked the speaker one short question.\n", overlapMarker)
	b.WriteString("- To speak, call utterance_say. It is the only way to be heard: text in this chat reaches nobody in the meeting; only the log of the runner on this computer shows it.\n")
	b.WriteString("- If nothing is for you, end your turn without text. Silence is normal; do not announce it.\n")
	b.WriteString("- When I tell you that sentences went off the record, never use or repeat them, and take them out of everything you wrote in this meeting at once.\n")
	fmt.Fprintf(&b, "- The local log of the session is the file %s (one JSON line per event). Read it if you need earlier events.\n",
		r.m.joined.Paths.Log)
	b.WriteString("\nHow to take part, unless your role says otherwise:\n")
	b.WriteString("- If someone addresses you and their turn has ended, answer directly. If their turn has not ended yet, answer now only if they asked you to; otherwise raise your hand with hand_raise. If their last sentence breaks off, they are probably not done: say nothing yet.\n")
	b.WriteString("- If you want to contribute without being addressed, raise your hand with hand_raise first, and speak when someone addresses you. If the conversation has moved on by then, drop what you wanted to say.\n")
	b.WriteString("- Speak only when you add something new. Do not repeat or summarise what others said, and do not just agree. If someone else already said it, stay silent.\n")
	b.WriteString("- Contribute unasked only when it changes something: a statement contradicts a recorded decision, a fact is wrong, or missing knowledge would change a decision.\n")
	b.WriteString("- Speak as in a conversation: the answer first, in one to three short sentences, in the language of the meeting. No Markdown, lists, paths or IDs. Put details into a document, show it with stage_focus, and say one sentence about it.\n")
	b.WriteString("- Answer an agent only if it addressed you. Humans go first.\n")
	b.WriteString("- Bring in only what the artifacts of the tenant say, and what follows from them. Say so when you find nothing; never guess. If someone asks where you know it from, name the artifact.\n")
	b.WriteString("- Advise, do not decide: name the options and their trade-offs. The humans decide.\n")
	b.WriteString("- If you said something wrong, correct it at once in one sentence.\n")
	b.WriteString("\nWhat you write:\n")
	b.WriteString("- If you take responsibility for an artifact of this meeting, for example the minutes or the ingest into the wiki, say so in the meeting as soon as you take it on, so that nobody else does it too. If someone already did, leave it to them.\n")
	b.WriteString("- When you have created or changed an artifact of this meeting (a document, slides, a decision), show it on the stage with stage_focus and say in one sentence what you changed. Do not take the stage while someone else presents; wait until they are done. stage_focus with \"minutes\" brings the minutes back.\n")
	fmt.Fprintf(&b, "- Everything you write that comes from this meeting (notes, decisions, documents, slides) must name the session %q. It waits until a human releases the meeting. Tasks are not held: they are shared at once, so keep sentences of the meeting out of them.\n", r.o.Session)
	if restarted {
		b.WriteString("\nThe harness that ran you stopped with an error, and I started it again. You remember nothing of your earlier turns in this chat, but the meeting is not new: do not repeat what your role asks you to do at the start of the meeting. ")
		b.WriteString("If your role keeps a document, for example the minutes, read its current state with the tools of the office before you change it, so that you do not replace it with less. ")
		b.WriteString("If you need earlier events, read the local log. If I have not sent you anything new yet, end your turn without text. If the meeting has ended meanwhile, I tell you in my next message.")
		return b.String()
	}
	b.WriteString("\nIf your role asks you to do something at the start of the meeting, do it now. Otherwise end your turn.")
	return b.String()
}

// modelWakes reports whether a model of the wake strategy judges the meeting, so that the
// prompts can carry whom the speaker addressed and the end of a block of topics.
func (r *runner) modelWakes() bool {
	return r.o.Wake.Strategy != "" && r.o.Wake.Strategy != WakeTurnEnd
}

// writeBatch writes the utterances of a batch, one per line, and the sentences that went off the
// record. Data only: the instructions are in the first prompt.
func (r *runner) writeBatch(b *strings.Builder, bt batch) {
	if len(bt.utterances) == 0 {
		b.WriteString("Nobody said anything new.\n")
	}
	for _, u := range bt.utterances {
		p := r.who(u.pid)
		hint := ""
		if u.overlap {
			hint = " " + overlapMarker
		}
		fmt.Fprintf(b, "[%d] %s %s (%s): %s%s\n", u.id, u.at.Local().Format(time.TimeOnly), p.name, p.kind,
			strings.Join(strings.Fields(u.text), " "), hint)
	}
	if len(bt.offNotices) > 0 {
		ids := make([]string, len(bt.offNotices))
		for i, id := range bt.offNotices {
			ids[i] = fmt.Sprintf("[%d]", id)
		}
		fmt.Fprintf(b, "\nTaken off the record afterwards: %s.\n", strings.Join(ids, ", "))
	}
}

// wakePrompt is the prompt that wakes the agent (dec-wake-check-per-utterance). It carries data
// only: what was said, whether the speaker has ended their turn, and what the model of the wake
// strategy estimated: whom the speaker addressed and whether a block of topics has ended.
func (r *runner) wakePrompt(bt batch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "New in the meeting %q since my last message:\n\n", r.o.Session)
	r.writeBatch(&b, bt)
	var facts []string
	if bt.turnBy != "" {
		p := r.who(bt.turnBy)
		facts = append(facts, fmt.Sprintf("The turn of %s (%s) has ended.", p.name, p.kind))
	} else if n := len(bt.utterances); n > 0 {
		p := r.who(bt.utterances[n-1].pid)
		facts = append(facts, fmt.Sprintf("The turn of %s (%s) has not ended yet.", p.name, p.kind))
	}
	if bt.addressed != "" {
		facts = append(facts, fmt.Sprintf("The model of the runner estimates that the speaker addressed: %s.", bt.addressed))
	}
	if bt.topicClosed {
		facts = append(facts, "The model of the runner estimates that a block of topics has ended.")
	}
	if len(facts) > 0 {
		b.WriteString("\n" + strings.Join(facts, "\n"))
	}
	return strings.TrimRight(b.String(), "\n")
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
	b.WriteString("If your role has work for the end of the meeting, for example the minutes, decisions or tasks, do it now. Do not speak in the meeting any more: it has ended and takes no new utterances. ")
	b.WriteString("Then go through what you kept in your own memory during this meeting, as the skill office-memory says. Then end your turn. This is my last message; I stop after it.")
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
