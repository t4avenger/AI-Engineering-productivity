package ui

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func (s *Server) populateSessionDetailEvidence(r *http.Request, id string, data *sessionDetailData) {
	events, err := s.listSessionEvents(r, id)
	if err != nil {
		data.GovernanceError = "Session governance checks are unavailable because retained events could not be loaded."
		data.RiskyAccess, data.UnapprovedMCP = unavailableGovernanceChecklist()
		data.FilesError = "File evidence is unavailable because retained events could not be loaded."
		data.ConversationError = "Retained conversation evidence is unavailable because session events could not be loaded."
		data.SpansError = "Span evidence is unavailable because retained events could not be loaded."
		return
	}
	data.inspectorSessionEvents = events
	s.populateConversationEvidence(r, events, data)
	data.SpanEvidence, data.SpansPartial = spanEvidenceRows(events)
	data.RiskyAccess = governance.RiskyAccessFromEvents(events)
	data.UnapprovedMCP = governance.UnapprovedMCPFromEvents(events, s.currentMCPAllowlist())
	s.populateFileEvidence(r, id, events, data)
}

func (s *Server) populateConversationEvidence(r *http.Request, events []canonical.Event, data *sessionDetailData) {
	page, next, err := conversationRows(events, r.URL.Query().Get("conversation_cursor"))
	if err != nil {
		data.ConversationError = "Retained conversation evidence could not be loaded because its cursor is invalid."
		return
	}
	data.Conversation = page
	data.ConversationNext = next
}

func (s *Server) populateFileEvidence(r *http.Request, id string, events []canonical.Event, data *sessionDetailData) {
	operations := []canonical.Operation{}
	if s.operations != nil {
		listed, err := s.operations.ListOperations(r.Context(), storage.OperationFilter{SessionID: id})
		if err != nil {
			data.FilesError = "File evidence is unavailable because retained operations could not be loaded."
			return
		}
		operations = listed
	}
	data.FileEvidence = fileEvidenceRows(insights.SessionFilesFromEvidence(events, operations))
}
