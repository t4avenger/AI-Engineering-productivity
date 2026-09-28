package claude

import (
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// eventTypeMCPCall is the correlation event emitted for an MCP tool call observed
// in the session JSONL transcript. It carries the server/tool identity the
// MCP-inventory insight consumes to mark a connected server as actually used,
// matching the shape insights.mcpUseEvent expects (EventType "mcp_call"). The
// generic tool-call machinery it shares (collector, content decode, dispatch)
// lives in jsonl_toolcalls.go; this file is the MCP-specific shaping layer.
const eventTypeMCPCall = "mcp_call"

// mcpCorrelationEvent builds the canonical mcp_call event the MCP-inventory insight
// reads. It carries only the server/tool identity and correlation linkage — never
// the arguments or result body — so the raw arguments and result ride on the
// Operation instead.
func (c transcriptToolCall) mcpCorrelationEvent(receivedAt time.Time) canonical.Event {
	eventID := c.sessionID + ":mcp:" + c.toolUseID
	call := mcpCallExtension(c.server, c.toolName)
	call["tool_use_id"] = c.toolUseID
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       eventID,
		EventType:     eventTypeMCPCall,
		OccurredAt:    c.occurredAt,
		ReceivedAt:    receivedAt.UTC(),
		Provider:      provider,
		Tool:          tool,
		SourceSchema:  sourceSchemaTranscript,
		SourceVersion: fallbackString(c.version, unavailable),
		ActorID:       unavailable,
		DeviceID:      unavailable,
		SessionID:     c.sessionID,
		PrivacyLevel:  "operational",
		Attributes: map[string]any{
			"category":           string(canonical.OperationCategoryMCPCall),
			"unavailable_fields": mcpCallUnavailableFields(),
		},
		ProviderExtensions: map[string]any{
			"correlation": transcriptCorrelation(eventID, c.occurredAt, c.uuid, c.parentUUID),
			"mcp_call":    call,
		},
	}
}

// mcpOperation builds the canonical Operation for an MCP tool call. The operation
// ID shares the :tool:<tool_use_id> namespace with the OTLP tool_result operation,
// so the same call observed on both ingest routes correlates to one record rather
// than double-counting. The raw arguments and result ride under
// provider_extensions.mcp_call (epic #87 — MCP arguments and results are captured
// raw); an absent argument or result is omitted, never fabricated.
func (c transcriptToolCall) mcpOperation() canonical.Operation {
	operationID := c.sessionID + ":tool:" + c.toolUseID
	call := mcpCallExtension(c.server, c.toolName)
	call["tool_use_id"] = c.toolUseID
	if c.input != nil {
		call["arguments"] = c.input
	}
	if c.result != nil {
		call["result"] = c.result
	}
	c.addResultMeta(call)
	event := map[string]any{"tool_name": c.fullName, "tool_use_id": c.toolUseID}
	c.addCwd(event)
	return canonical.Operation{
		SchemaVersion: canonical.RecordSchemaVersion,
		OperationID:   operationID,
		SessionID:     c.sessionID,
		Provider:      provider,
		Tool:          tool,
		Category:      canonical.OperationCategoryMCPCall,
		Outcome:       c.outcome,
		Provenance:    canonical.ProvenanceObserved,
		ProviderExtensions: map[string]any{
			"correlation": operationCorrelation(operationID, c.occurredAt, transcriptTaskBoundaryReason),
			"event":       event,
			"mcp_call":    call,
		},
	}
}

// mcpCallUnavailableFields lists the behaviour signals an mcp_call correlation
// event does not carry, so an absent signal is explicit rather than silently
// missing. The event proves the MCP call happened (mcp_calls / tool_calls are
// available and so absent from this list); it carries no model, token, or content
// identity of its own — those live on the api_request and assistant records. The
// generic tool_call event derives its own list from this one (toolCallUnavailableFields).
func mcpCallUnavailableFields() []string {
	return []string{
		"model",
		"token_usage",
		"cache_usage",
		"task_outcome",
		"reasoning_tokens",
		"file_operations",
		"repository_context",
		"prompt_content",
		"response_content",
		"provider_cost",
		"trace_span_correlation",
		"approvals",
	}
}

// ExtractTranscriptOperations reconstructs tool-call Operations from a Claude Code
// session JSONL transcript (#104 MCP calls, #105 generic tool IO), the sibling of
// NormalizeTranscript's event path and a mirror of
// log_operations.go:ExtractLogOperations. It walks the transcript once, pairing
// each tool_use with its tool_result by tool_use_id, and emits one Operation per
// invocation (an MCP-call operation for mcp__* tools, a generic tool_call
// operation otherwise). A transcript with no tool calls yields an empty slice and
// no error.
func ExtractTranscriptOperations(data []byte, _ time.Time) ([]canonical.Operation, error) {
	_, calls, err := walkTranscript(data, time.Time{})
	if err != nil {
		return nil, err
	}
	operations := make([]canonical.Operation, 0, len(calls))
	for _, call := range calls {
		operations = append(operations, call.operation())
	}
	return normalize.CorrelateOperations(operations), nil
}
