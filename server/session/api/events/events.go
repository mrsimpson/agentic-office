// Package events knows the dotted type names of session events (concept-event-log).
package events

import sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"

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
	ArtifactShown         = "artifact.shown"
	ParticipantJoined     = "participant.joined"
	ParticipantLeft       = "participant.left"
	ParticipantPaused     = "participant.paused"
	ParticipantResumed    = "participant.resumed"
	SessionEnded          = "session.ended"
)

// TypeOf returns the dotted type that belongs to the payload of ev, or "" if it has none.
func TypeOf(ev *sessionv1.Event) string {
	switch ev.GetPayload().(type) {
	case *sessionv1.Event_UtteranceFinal:
		return UtteranceFinal
	case *sessionv1.Event_UtteranceOffTheRecord:
		return UtteranceOffTheRecord
	case *sessionv1.Event_UtteranceCorrected:
		return UtteranceCorrected
	case *sessionv1.Event_TurnEnd:
		return TurnEnd
	case *sessionv1.Event_SilenceLong:
		return SilenceLong
	case *sessionv1.Event_Addressed:
		return Addressed
	case *sessionv1.Event_HandRaised:
		return HandRaised
	case *sessionv1.Event_MinutesUpdated:
		return MinutesUpdated
	case *sessionv1.Event_ArtifactShown:
		return ArtifactShown
	case *sessionv1.Event_ParticipantJoined:
		return ParticipantJoined
	case *sessionv1.Event_ParticipantLeft:
		return ParticipantLeft
	case *sessionv1.Event_ParticipantPaused:
		return ParticipantPaused
	case *sessionv1.Event_ParticipantResumed:
		return ParticipantResumed
	case *sessionv1.Event_SessionEnded:
		return SessionEnded
	}
	return ""
}
