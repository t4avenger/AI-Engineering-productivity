package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

var (
	// ErrMalformedRollout identifies invalid NDJSON syntax. Callers may map it
	// to HTTP 400 without exposing provider content in an error response.
	ErrMalformedRollout = errors.New("malformed Codex rollout")
	// ErrInvalidRollout identifies valid JSON that lacks the provider-native
	// identity needed for exact correlation or another required envelope field.
	ErrInvalidRollout = errors.New("invalid Codex rollout")
)

const sourceSchemaRollout = "codex_rollout_jsonl"

type rolloutRecord struct {
	line       int
	raw        []byte
	decoded    map[string]any
	recordType string
	timestamp  time.Time
}

type rolloutMetadata struct {
	sessionID     string
	sourceVersion string
	actorID       string
}

// NormalizeRollout retains every record in a Codex on-disk rollout JSONL file.
// Only observed user and assistant message shapes receive canonical conversation
// event types; all other records retain the provider's discriminator verbatim in
// provider_extensions and use a generic rollout event type.
func NormalizeRollout(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	records, metadata, err := decodeRollout(data)
	if err != nil {
		return nil, err
	}
	events := make([]canonical.Event, 0, len(records))
	for _, record := range records {
		events = append(events, rolloutEvent(record, metadata, receivedAt))
	}
	return normalize.CorrelateEvents(events), nil
}

func decodeRollout(data []byte) ([]rolloutRecord, rolloutMetadata, error) {
	var records []rolloutRecord
	metadata := rolloutMetadata{sourceVersion: unavailable, actorID: unavailable}
	lineNumber := 0
	for remaining := data; len(remaining) > 0; {
		lineNumber++
		line, rest := nextRolloutLine(remaining)
		remaining = rest
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		record, err := decodeRolloutRecord(trimmed, lineNumber)
		if err != nil {
			return nil, rolloutMetadata{}, err
		}
		records = append(records, record)
		if record.recordType == "session_meta" {
			if err := metadata.observeSessionMeta(record.decoded); err != nil {
				return nil, rolloutMetadata{}, err
			}
		}
	}
	if len(records) == 0 {
		return nil, rolloutMetadata{}, fmt.Errorf("%w: no records", ErrMalformedRollout)
	}
	if metadata.sessionID == "" {
		return nil, rolloutMetadata{}, fmt.Errorf("%w: session_meta.payload.id is required", ErrInvalidRollout)
	}
	return records, metadata, nil
}

func nextRolloutLine(remaining []byte) (line, rest []byte) {
	if index := bytes.IndexByte(remaining, '\n'); index >= 0 {
		return remaining[:index], remaining[index+1:]
	}
	return remaining, nil
}

func decodeRolloutRecord(line []byte, lineNumber int) (rolloutRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return rolloutRecord{}, fmt.Errorf("%w: line %d", ErrMalformedRollout, lineNumber)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return rolloutRecord{}, fmt.Errorf("%w: line %d", ErrMalformedRollout, lineNumber)
	}
	recordType, err := normalize.RequiredString(decoded, "type")
	if err != nil {
		return rolloutRecord{}, fmt.Errorf("%w: line %d type", ErrInvalidRollout, lineNumber)
	}
	timestampText, err := normalize.RequiredString(decoded, "timestamp")
	if err != nil {
		return rolloutRecord{}, fmt.Errorf("%w: line %d timestamp", ErrInvalidRollout, lineNumber)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	if err != nil {
		return rolloutRecord{}, fmt.Errorf("%w: line %d timestamp", ErrInvalidRollout, lineNumber)
	}
	return rolloutRecord{
		line: lineNumber, raw: append([]byte(nil), line...), decoded: decoded,
		recordType: recordType, timestamp: timestamp,
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra JSON value")
	}
	return nil
}

func (metadata *rolloutMetadata) observeSessionMeta(record map[string]any) error {
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: session_meta payload", ErrInvalidRollout)
	}
	sessionID, err := normalize.RequiredString(payload, "id")
	if err != nil {
		return fmt.Errorf("%w: session_meta.payload.id", ErrInvalidRollout)
	}
	if metadata.sessionID != "" && metadata.sessionID != sessionID {
		return fmt.Errorf("%w: conflicting session identifiers", ErrInvalidRollout)
	}
	metadata.sessionID = sessionID
	if version, ok := normalize.ObservedString(payload["cli_version"]); ok {
		metadata.sourceVersion = version
	}
	if actorID, ok := normalize.ObservedString(payload["creator_user_id"]); ok {
		metadata.actorID = actorID
	}
	return nil
}

func rolloutEvent(record rolloutRecord, metadata rolloutMetadata, receivedAt time.Time) canonical.Event {
	eventType, echo := rolloutConversation(record.decoded)
	if eventType == "" {
		eventType = "codex.rollout." + record.recordType
	}
	extensions := map[string]any{
		"rollout": map[string]any{
			"line":        record.line,
			"record_type": record.recordType,
			"record":      record.decoded,
		},
	}
	if echo != nil {
		extensions["event"] = echo
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            rolloutEventID(record, metadata.sessionID),
		EventType:          eventType,
		OccurredAt:         record.timestamp.UTC(),
		ReceivedAt:         receivedAt.UTC(),
		Provider:           "openai",
		Tool:               "codex",
		SourceSchema:       sourceSchemaRollout,
		SourceVersion:      metadata.sourceVersion,
		ActorID:            metadata.actorID,
		DeviceID:           unavailable,
		SessionID:          normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID),
		PrivacyLevel:       "content",
		Attributes:         map[string]any{"unavailable_fields": []string{}},
		ProviderExtensions: extensions,
	}
}

func rolloutEventID(record rolloutRecord, sessionID string) string {
	if payload, ok := record.decoded["payload"].(map[string]any); ok {
		if id, ok := normalize.ObservedString(payload["id"]); ok {
			return fmt.Sprintf("codex-rollout:%s:%s:%s", sessionID, record.recordType, id)
		}
	}
	digest := sha256.Sum256(record.raw)
	return fmt.Sprintf("codex-rollout:%s:%d:%s", sessionID, record.line, hex.EncodeToString(digest[:]))
}

func rolloutConversation(record map[string]any) (string, map[string]any) {
	if recordType, _ := record["type"].(string); recordType != "response_item" {
		return "", nil
	}
	payload, ok := record["payload"].(map[string]any)
	if !ok || payload["type"] != "message" {
		return "", nil
	}
	role, _ := payload["role"].(string)
	text, ok := rolloutMessageText(payload["content"])
	if !ok {
		return "", nil
	}
	switch role {
	case "user":
		return "user_prompt", map[string]any{"prompt": text}
	case "assistant":
		return "assistant_response", map[string]any{"response": text}
	default:
		return "", nil
	}
}

func rolloutMessageText(value any) (string, bool) {
	blocks, ok := value.([]any)
	if !ok {
		return "", false
	}
	text := make([]string, 0, len(blocks))
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := block["type"].(string)
		if kind != "input_text" && kind != "output_text" {
			continue
		}
		if body, ok := block["text"].(string); ok {
			text = append(text, body)
		}
	}
	if len(text) == 0 {
		return "", false
	}
	return strings.Join(text, "\n"), true
}
