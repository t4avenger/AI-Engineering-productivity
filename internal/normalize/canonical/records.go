package canonical

import "time"

// recordSchemaVersion is the independent schema version for the stable-primitive
// records. It tracks each record's own contract, not the Event envelope's.
const recordSchemaVersion = "0.1.0"

// Provenance marks how a field or record was established: directly observed from
// provider telemetry, inferred by TelemetryIQ, or unknown.
type Provenance string

const (
	ProvenanceObserved Provenance = "observed"
	ProvenanceInferred Provenance = "inferred"
	ProvenanceUnknown  Provenance = "unknown"
)

// OperationCategory is the provider-independent category of a tool invocation,
// mirroring PRODUCT_MAP.md §10.5. Provider-specific tool names are stored
// separately (in Operation.Tool / ProviderExtensions).
type OperationCategory string

const (
	OperationCategoryFilesystemRead   OperationCategory = "filesystem read"
	OperationCategoryFilesystemWrite  OperationCategory = "filesystem write"
	OperationCategoryFilesystemDelete OperationCategory = "filesystem delete"
	OperationCategoryShellCommand     OperationCategory = "shell command"
	OperationCategoryNetworkRequest   OperationCategory = "network request"
	OperationCategoryBrowserAction    OperationCategory = "browser action"
	OperationCategoryMCPCall          OperationCategory = "MCP call"
	OperationCategorySourceControl    OperationCategory = "source-control action"
	OperationCategoryTestExecution    OperationCategory = "test execution"
	OperationCategoryBuildExecution   OperationCategory = "build execution"
	OperationCategoryDeploymentAction OperationCategory = "deployment action"
	OperationCategoryUnknown          OperationCategory = "unknown"
)

// ModelInteraction is a stable-primitive record of one model request/response,
// at schema version 0.1.0 (recordSchemaVersion). It carries only stable
// primitives shared across providers; cost is deliberately excluded per the
// reorientation (roadmap §0). Token categories are nullable so an absent value
// is distinguishable from a genuine zero. Anything provider-specific stays in
// ProviderExtensions until two providers validate identical semantics.
type ModelInteraction struct {
	SchemaVersion      string         `json:"schema_version"`
	RequestID          string         `json:"request_id"`
	SessionID          string         `json:"session_id"`
	Provider           string         `json:"provider"`
	Tool               string         `json:"tool"`
	Model              string         `json:"model"`
	StartedAt          time.Time      `json:"started_at"`
	CompletedAt        time.Time      `json:"completed_at"`
	DurationMs         *int64         `json:"duration_ms"`
	InputTokens        *int64         `json:"input_tokens"`
	OutputTokens       *int64         `json:"output_tokens"`
	CachedInputTokens  *int64         `json:"cached_input_tokens"`
	ReasoningTokens    *int64         `json:"reasoning_tokens"`
	Result             string         `json:"result"`
	ErrorCode          string         `json:"error_code"`
	Provenance         Provenance     `json:"provenance"`
	ProviderExtensions map[string]any `json:"provider_extensions"`
}

// Operation is a stable-primitive record of one tool invocation, at schema
// version 0.1.0 (recordSchemaVersion). Category is drawn from OperationCategory
// (§10.5) and is stored independently of the provider-specific Tool name.
// Provider-specific detail (MCP/skill/file specifics) stays in ProviderExtensions
// until two providers validate identical semantics.
type Operation struct {
	SchemaVersion      string            `json:"schema_version"`
	OperationID        string            `json:"operation_id"`
	SessionID          string            `json:"session_id"`
	Provider           string            `json:"provider"`
	Tool               string            `json:"tool"`
	Category           OperationCategory `json:"category"`
	Outcome            string            `json:"outcome"`
	Provenance         Provenance        `json:"provenance"`
	ProviderExtensions map[string]any    `json:"provider_extensions"`
}
