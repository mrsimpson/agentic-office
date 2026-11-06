// Package events knows the dotted type names of session events (concept-event-log).
package events

import officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"

// Event types in dotted form.
const (
	UtteranceFinal     = "utterance.final"
	UtteranceRetracted = "utterance.retracted"
	UtteranceCorrected = "utterance.corrected"
	TurnEnd            = "turn.end"
	SilenceLong        = "silence.long"
	Addressed          = "addressed"
	HandRaised         = "hand.raised"
	MinutesUpdated     = "minutes.updated"
	ArtifactShown      = "artifact.shown"
	ParticipantJoined  = "participant.joined"
	ParticipantLeft    = "participant.left"
	ParticipantPaused  = "participant.paused"
	ParticipantResumed = "participant.resumed"
	SessionEnded       = "session.ended"
)

// TypeOf returns the dotted type that belongs to the payload of ev, or "" if it has none.
func TypeOf(ev *officev1.Event) string {
	switch ev.GetPayload().(type) {
	case *officev1.Event_UtteranceFinal:
		return UtteranceFinal
	case *officev1.Event_UtteranceRetracted:
		return UtteranceRetracted
	case *officev1.Event_UtteranceCorrected:
		return UtteranceCorrected
	case *officev1.Event_TurnEnd:
		return TurnEnd
	case *officev1.Event_SilenceLong:
		return SilenceLong
	case *officev1.Event_Addressed:
		return Addressed
	case *officev1.Event_HandRaised:
		return HandRaised
	case *officev1.Event_MinutesUpdated:
		return MinutesUpdated
	case *officev1.Event_ArtifactShown:
		return ArtifactShown
	case *officev1.Event_ParticipantJoined:
		return ParticipantJoined
	case *officev1.Event_ParticipantLeft:
		return ParticipantLeft
	case *officev1.Event_ParticipantPaused:
		return ParticipantPaused
	case *officev1.Event_ParticipantResumed:
		return ParticipantResumed
	case *officev1.Event_SessionEnded:
		return SessionEnded
	}
	return ""
}
