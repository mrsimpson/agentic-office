package main

// The content services of the hub are one instance for each tenant (tenantRuntime). These
// handlers pick the instance of the tenant of the call and pass the call on. The identity
// interceptor has checked the membership before (concept-multi-tenancy).

import (
	"context"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	commandsapi "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	importapi "github.com/mrsimpson/agentic-office/hub/session/importapi"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	streamapi "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
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
