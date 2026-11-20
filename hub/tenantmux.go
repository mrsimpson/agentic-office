package main

// The content services of the hub are one instance for each tenant (tenantRuntime). These
// handlers pick the instance of the tenant of the call and pass the call on. The identity
// interceptor has checked the membership before (concept-multi-tenancy).

import (
	"context"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	"github.com/mrsimpson/agentic-office/hub/archive/changes/api/changesv1connect"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	activityapi "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	"github.com/mrsimpson/agentic-office/hub/session/activityapi/sessionactivityv1connect"
	commandsapi "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	importapi "github.com/mrsimpson/agentic-office/hub/session/importapi"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	listapi "github.com/mrsimpson/agentic-office/hub/session/listapi"
	"github.com/mrsimpson/agentic-office/hub/session/listapi/sessionlistv1connect"
	streamapi "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
	wikiv1 "github.com/mrsimpson/agentic-office/hub/wiki/api"
	"github.com/mrsimpson/agentic-office/hub/wiki/api/wikiv1connect"
)

type commandsMux struct{ *runtimes }

var _ sessioncommandsv1connect.SessionCommandsServiceHandler = commandsMux{}

func (m commandsMux) Join(ctx context.Context, req *commandsapi.JoinRequest) (*commandsapi.JoinResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.Join(ctx, req)
}

func (m commandsMux) Leave(ctx context.Context, req *commandsapi.LeaveRequest) (*commandsapi.LeaveResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.Leave(ctx, req)
}

func (m commandsMux) SubmitUtterance(ctx context.Context, req *commandsapi.SubmitUtteranceRequest) (*commandsapi.SubmitUtteranceResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.SubmitUtterance(ctx, req)
}

func (m commandsMux) MarkOffTheRecord(ctx context.Context, req *commandsapi.MarkOffTheRecordRequest) (*commandsapi.MarkOffTheRecordResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.MarkOffTheRecord(ctx, req)
}

func (m commandsMux) SubmitSignal(ctx context.Context, req *commandsapi.SubmitSignalRequest) (*commandsapi.SubmitSignalResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.SubmitSignal(ctx, req)
}

func (m commandsMux) Pause(ctx context.Context, req *commandsapi.PauseRequest) (*commandsapi.PauseResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.Pause(ctx, req)
}

func (m commandsMux) Resume(ctx context.Context, req *commandsapi.ResumeRequest) (*commandsapi.ResumeResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.Resume(ctx, req)
}

func (m commandsMux) RaiseHand(ctx context.Context, req *commandsapi.RaiseHandRequest) (*commandsapi.RaiseHandResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.RaiseHand(ctx, req)
}

func (m commandsMux) UpdateMinutes(ctx context.Context, req *commandsapi.UpdateMinutesRequest) (*commandsapi.UpdateMinutesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.UpdateMinutes(ctx, req)
}

func (m commandsMux) FocusArtifact(ctx context.Context, req *commandsapi.FocusArtifactRequest) (*commandsapi.FocusArtifactResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.FocusArtifact(ctx, req)
}

func (m commandsMux) EndSession(ctx context.Context, req *commandsapi.EndSessionRequest) (*commandsapi.EndSessionResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.EndSession(ctx, req)
}

func (m commandsMux) ReleaseSession(ctx context.Context, req *commandsapi.ReleaseSessionRequest) (*commandsapi.ReleaseSessionResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.ReleaseSession(ctx, req)
}

func (m commandsMux) ObjectToRelease(ctx context.Context, req *commandsapi.ObjectToReleaseRequest) (*commandsapi.ObjectToReleaseResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.ObjectToRelease(ctx, req)
}

func (m commandsMux) GetRelease(ctx context.Context, req *commandsapi.GetReleaseRequest) (*commandsapi.GetReleaseResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetRelease(ctx, req)
}

func (m commandsMux) GetTreeHead(ctx context.Context, req *commandsapi.GetTreeHeadRequest) (*commandsapi.GetTreeHeadResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetTreeHead(ctx, req)
}

func (m commandsMux) GetConsistencyProof(ctx context.Context, req *commandsapi.GetConsistencyProofRequest) (*commandsapi.GetConsistencyProofResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetConsistencyProof(ctx, req)
}

func (m commandsMux) GetInclusionProof(ctx context.Context, req *commandsapi.GetInclusionProofRequest) (*commandsapi.GetInclusionProofResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetInclusionProof(ctx, req)
}

func (m commandsMux) GetHubKey(ctx context.Context, req *commandsapi.GetHubKeyRequest) (*commandsapi.GetHubKeyResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetHubKey(ctx, req)
}

func (m commandsMux) OfferLog(ctx context.Context, req *commandsapi.OfferLogRequest) (*commandsapi.OfferLogResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.OfferLog(ctx, req)
}

type importMux struct{ *runtimes }

var _ sessionimportv1connect.SessionImportServiceHandler = importMux{}

func (m importMux) ImportConversation(ctx context.Context, req *importapi.ImportConversationRequest) (*importapi.ImportConversationResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.ImportConversation(ctx, req)
}

type activityMux struct{ *runtimes }

var _ sessionactivityv1connect.SessionActivityServiceHandler = activityMux{}

func (m activityMux) ReportActivity(ctx context.Context, req *activityapi.ReportActivityRequest) (*activityapi.ReportActivityResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.ReportActivity(ctx, req)
}

func (m activityMux) WatchActivity(ctx context.Context, req *activityapi.WatchActivityRequest, stream sessionactivityv1connect.SessionActivityServiceWatchActivityServerStream) error {
	rt, err := m.forCall(ctx)
	if err != nil {
		return err
	}
	return rt.sessions.WatchActivity(ctx, req, stream)
}

type listMux struct{ *runtimes }

var _ sessionlistv1connect.SessionListServiceHandler = listMux{}

func (m listMux) ListSessions(ctx context.Context, req *listapi.ListSessionsRequest) (*listapi.ListSessionsResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.ListSessions(ctx, req)
}

type decisionMux struct{ *runtimes }

var _ decisionv1connect.DecisionServiceHandler = decisionMux{}

func (m decisionMux) RecordDecision(ctx context.Context, req *decisionv1.RecordDecisionRequest) (*decisionv1.RecordDecisionResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.RecordDecision(ctx, req)
}

func (m decisionMux) ListDecisions(ctx context.Context, req *decisionv1.ListDecisionsRequest) (*decisionv1.ListDecisionsResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ListDecisions(ctx, req)
}

func (m decisionMux) ChangeDecision(ctx context.Context, req *decisionv1.ChangeDecisionRequest) (*decisionv1.ChangeDecisionResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ChangeDecision(ctx, req)
}

func (m decisionMux) RemoveDecision(ctx context.Context, req *decisionv1.RemoveDecisionRequest) (*decisionv1.RemoveDecisionResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.RemoveDecision(ctx, req)
}

type minutesMux struct{ *runtimes }

var _ minutesv1connect.MinutesServiceHandler = minutesMux{}

func (m minutesMux) GetMinutes(ctx context.Context, req *minutesv1.GetMinutesRequest) (*minutesv1.GetMinutesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.GetMinutes(ctx, req)
}

func (m minutesMux) ListMinutes(ctx context.Context, req *minutesv1.ListMinutesRequest) (*minutesv1.ListMinutesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ListMinutes(ctx, req)
}

func (m minutesMux) ChangeMinutes(ctx context.Context, req *minutesv1.ChangeMinutesRequest) (*minutesv1.ChangeMinutesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ChangeMinutes(ctx, req)
}

func (m minutesMux) RemoveMinutes(ctx context.Context, req *minutesv1.RemoveMinutesRequest) (*minutesv1.RemoveMinutesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.RemoveMinutes(ctx, req)
}

type noteMux struct{ *runtimes }

var _ notev1connect.NoteServiceHandler = noteMux{}

func (m noteMux) AddNote(ctx context.Context, req *notev1.AddNoteRequest) (*notev1.AddNoteResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.AddNote(ctx, req)
}

func (m noteMux) ListNotes(ctx context.Context, req *notev1.ListNotesRequest) (*notev1.ListNotesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ListNotes(ctx, req)
}

func (m noteMux) ChangeNote(ctx context.Context, req *notev1.ChangeNoteRequest) (*notev1.ChangeNoteResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.ChangeNote(ctx, req)
}

func (m noteMux) RemoveNote(ctx context.Context, req *notev1.RemoveNoteRequest) (*notev1.RemoveNoteResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.records.RemoveNote(ctx, req)
}

type taskMux struct{ *runtimes }

var _ taskv1connect.TaskServiceHandler = taskMux{}

func (m taskMux) CreateTask(ctx context.Context, req *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.tasks.CreateTask(ctx, req)
}

func (m taskMux) GetTask(ctx context.Context, req *taskv1.GetTaskRequest) (*taskv1.GetTaskResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.tasks.GetTask(ctx, req)
}

func (m taskMux) ListTasks(ctx context.Context, req *taskv1.ListTasksRequest) (*taskv1.ListTasksResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.tasks.ListTasks(ctx, req)
}

func (m taskMux) UpdateTask(ctx context.Context, req *taskv1.UpdateTaskRequest) (*taskv1.UpdateTaskResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.tasks.UpdateTask(ctx, req)
}

type slidesMux struct{ *runtimes }

var _ slidesv1connect.SlidesServiceHandler = slidesMux{}

func (m slidesMux) RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.slides.RenderDeck(ctx, req)
}

func (m slidesMux) GetDeck(ctx context.Context, req *slidesv1.GetDeckRequest) (*slidesv1.GetDeckResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.slides.GetDeck(ctx, req)
}

func (m slidesMux) ChangeDeck(ctx context.Context, req *slidesv1.ChangeDeckRequest) (*slidesv1.ChangeDeckResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.slides.ChangeDeck(ctx, req)
}

func (m slidesMux) RemoveDeck(ctx context.Context, req *slidesv1.RemoveDeckRequest) (*slidesv1.RemoveDeckResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.slides.RemoveDeck(ctx, req)
}

type documentMux struct{ *runtimes }

var _ documentv1connect.DocumentServiceHandler = documentMux{}

func (m documentMux) CreateDocument(ctx context.Context, req *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.documents.CreateDocument(ctx, req)
}

func (m documentMux) ChangeDocument(ctx context.Context, req *documentv1.ChangeDocumentRequest) (*documentv1.ChangeDocumentResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.documents.ChangeDocument(ctx, req)
}

func (m documentMux) RemoveDocument(ctx context.Context, req *documentv1.RemoveDocumentRequest) (*documentv1.RemoveDocumentResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.documents.RemoveDocument(ctx, req)
}

type archiveReadMux struct{ *runtimes }

var _ archivev1connect.ArchiveReadServiceHandler = archiveReadMux{}

func (m archiveReadMux) ReadFile(ctx context.Context, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.archiveRead.ReadFile(ctx, req)
}

func (m archiveReadMux) ListFiles(ctx context.Context, req *archivev1.ListFilesRequest) (*archivev1.ListFilesResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.archiveRead.ListFiles(ctx, req)
}

type archiveChangesMux struct{ *runtimes }

var _ changesv1connect.ArchiveChangesServiceHandler = archiveChangesMux{}

func (m archiveChangesMux) Follow(ctx context.Context, req *changesv1.FollowRequest, stream changesv1connect.ArchiveChangesServiceFollowServerStream) error {
	rt, err := m.forCall(ctx)
	if err != nil {
		return err
	}
	return rt.archiveChanges.Follow(ctx, req, stream)
}

type streamMux struct{ *runtimes }

var _ sessionstreamv1connect.SessionStreamServiceHandler = streamMux{}

func (m streamMux) Subscribe(ctx context.Context, req *streamapi.SubscribeRequest, stream sessionstreamv1connect.SessionStreamServiceSubscribeServerStream) error {
	rt, err := m.forCall(ctx)
	if err != nil {
		return err
	}
	return rt.sessions.Subscribe(ctx, req, stream)
}

func (m streamMux) GetEvents(ctx context.Context, req *streamapi.GetEventsRequest) (*streamapi.GetEventsResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.sessions.GetEvents(ctx, req)
}

type wikiMux struct{ *runtimes }

var _ wikiv1connect.WikiServiceHandler = wikiMux{}

func (m wikiMux) ListArtifacts(ctx context.Context, req *wikiv1.ListArtifactsRequest) (*wikiv1.ListArtifactsResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.wiki.ListArtifacts(ctx, req)
}

func (m wikiMux) GetArtifact(ctx context.Context, req *wikiv1.GetArtifactRequest) (*wikiv1.GetArtifactResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.wiki.GetArtifact(ctx, req)
}

func (m wikiMux) ListBrokenLinks(ctx context.Context, req *wikiv1.ListBrokenLinksRequest) (*wikiv1.ListBrokenLinksResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.wiki.ListBrokenLinks(ctx, req)
}

func (m wikiMux) ListIngestQueue(ctx context.Context, req *wikiv1.ListIngestQueueRequest) (*wikiv1.ListIngestQueueResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.wiki.ListIngestQueue(ctx, req)
}

func (m wikiMux) Search(ctx context.Context, req *wikiv1.SearchRequest) (*wikiv1.SearchResponse, error) {
	rt, err := m.forCall(ctx)
	if err != nil {
		return nil, err
	}
	return rt.wiki.Search(ctx, req)
}
