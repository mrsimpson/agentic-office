// Package events knows the dotted type names of session events (concept-event-log).
package events

import sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"

// Event types in dotted form.
const (
	UtteranceFinal        = "utterance.final"
	UtteranceOffTheRecord = "utterance.off_the_record"
	UtteranceCorrected    = "utterance.corrected"
	TurnEnd               = "turn.end"
	SilenceLong           = "silence.long"
	Addressed             = "addressed"
	HandRaised            = "hand.raised"
	MinutesUpdated        = "minutes.updated"
	ArtifactCreated       = "artifact.created"
	ArtifactChanged       = "artifact.changed"
	ArtifactFocused       = "artifact.focused"
	ParticipantJoined     = "participant.joined"
	ParticipantLeft       = "participant.left"
	ParticipantPaused     = "participant.paused"
	ParticipantResumed    = "participant.resumed"
	SessionEnded          = "session.ended"
	SessionImported       = "session.imported"
	ImportCompleted       = "import.completed"
)

// TypeOf returns the dotted type that belongs to the payload of ev, or "" if it has none.
func TypeOf(ev *sessionstreamv1.Event) string {
	switch ev.GetPayload().(type) {
	case *sessionstreamv1.Event_UtteranceFinal:
		return UtteranceFinal
	case *sessionstreamv1.Event_UtteranceOffTheRecord:
		return UtteranceOffTheRecord
	case *sessionstreamv1.Event_UtteranceCorrected:
		return UtteranceCorrected
	case *sessionstreamv1.Event_TurnEnd:
		return TurnEnd
	case *sessionstreamv1.Event_SilenceLong:
		return SilenceLong
	case *sessionstreamv1.Event_Addressed:
		return Addressed
	case *sessionstreamv1.Event_HandRaised:
		return HandRaised
	case *sessionstreamv1.Event_MinutesUpdated:
		return MinutesUpdated
	case *sessionstreamv1.Event_ArtifactFocused:
		return ArtifactFocused
	case *sessionstreamv1.Event_ArtifactCreated:
		return ArtifactCreated
	case *sessionstreamv1.Event_ArtifactChanged:
		return ArtifactChanged
	case *sessionstreamv1.Event_ParticipantJoined:
		return ParticipantJoined
	case *sessionstreamv1.Event_ParticipantLeft:
		return ParticipantLeft
	case *sessionstreamv1.Event_ParticipantPaused:
		return ParticipantPaused
	case *sessionstreamv1.Event_ParticipantResumed:
		return ParticipantResumed
	case *sessionstreamv1.Event_SessionEnded:
		return SessionEnded
	case *sessionstreamv1.Event_SessionImported:
		return SessionImported
	case *sessionstreamv1.Event_ImportCompleted:
		return ImportCompleted
	}
	return ""
}
