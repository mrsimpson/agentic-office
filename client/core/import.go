package core

import (
	"context"
	"fmt"

	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// Import imports a conversation as a new session (dec-session-import). sessionID may be empty: then
// Import creates one ("import-" plus a new ID). Each page gets its own command ID; a page that is
// sent again after a dropped token is not stored twice.
func (c *hubClient) Import(ctx context.Context, sessionID string, conv *coreapi.Conversation) (*coreapi.Imported, error) {
	pages, err := conv.Pages()
	if err != nil {
		return nil, err
	}
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		sessionID = "import-" + coreapi.NewCommandID()
	}
	out := &coreapi.Imported{SessionID: sessionID, Pages: len(pages), Participants: len(conv.Participants),
		Utterances: len(conv.Utterances)}
	for i, p := range pages {
		p.SessionId, p.CommandId, p.ParticipantId = sessionID, coreapi.NewCommandID(), pid
		var res *sessionimportv1.ImportConversationResponse
		err := retry(ctx, func() (err error) {
			res, err = c.sessionImport.ImportConversation(ctx, p)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("import page %d of %d into session %s: %w", i+1, len(pages), sessionID, err)
		}
		if i == 0 {
			out.FirstEventID = res.GetFirstEventId()
		}
		out.LastEventID, out.TreeHead = res.GetLastEventId(), res.GetTreeHead()
	}
	return out, nil
}
