package claude

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Residual raw echo (#263, epic #87). The transcript normaliser promotes a reviewed
// set of record fields into typed homes (correlation, the transcript envelope, the
// canonical token attributes, the content shapers, the tool Operation). Everything
// else on a user/assistant record is echoed raw under provider_extensions.
// transcript_record with its wire key names, so a field Claude Code adds later is
// retained without an allow-list change and nothing is dropped at ingest.
//
// A field counts as typed only when its typed home actually stored the exact
// value: an explicit null, an empty string, a wrong-typed value, or a value the
// typed home would trim or reformat stays in the residual instead of being lost.

// residualNode returns what is left of one raw value once its typed home has taken
// the parts it stores. ok is false when nothing is left.
type residualNode interface {
	residual(raw json.RawMessage) (left any, ok bool)
}

// scalarRule is a leaf whose value is fully retained by its typed home when stored
// reports true for the raw value.
type scalarRule func(raw json.RawMessage) bool

func (rule scalarRule) residual(raw json.RawMessage) (any, bool) {
	if rule(raw) {
		return nil, false
	}
	return decodeRawContent(raw), true
}

// objectRules walks a JSON object: keys with a rule are reduced by it, and every
// other key is echoed raw. A non-object value has no typed home and is echoed whole.
type objectRules map[string]residualNode

func (rules objectRules) residual(raw json.RawMessage) (any, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return decodeRawContent(raw), true
	}
	left := map[string]any{}
	for key, value := range fields {
		rule, typed := rules[key]
		if !typed {
			left[key] = decodeRawContent(value)
			continue
		}
		if residual, ok := rule.residual(value); ok {
			left[key] = residual
		}
	}
	return left, len(left) > 0
}

// contentRules reduces message.content. A bare string is typed when the record's
// shaper stores it (stringTyped); an array is walked block by block, and a block
// keeps a residual entry — {index, block} — only for the keys its type's shaper does
// not store, or whole when its type is unrecognised or its typed body is missing.
type contentRules struct {
	stringTyped bool
	blocks      map[string]objectRules
}

func (rules contentRules) residual(raw json.RawMessage) (any, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if rules.stringTyped && strings.TrimSpace(text) != "" {
			return nil, false
		}
		return text, true
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return decodeRawContent(raw), true
	}
	var left []any
	for index, block := range blocks {
		if residual, ok := rules.blockResidual(block); ok {
			left = append(left, map[string]any{"index": index, "block": residual})
		}
	}
	return left, len(left) > 0
}

// blockResidual reduces one content block by the rules for its type. type itself is
// typed by the block's typed home, but rides back on any residual entry so a
// leftover key is never separated from its block type.
func (rules contentRules) blockResidual(raw json.RawMessage) (any, bool) {
	var discriminator struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &discriminator)
	blockRules, known := rules.blocks[discriminator.Type]
	if !known {
		return decodeRawContent(raw), true
	}
	left, ok := blockRules.residual(raw)
	fields, isMap := left.(map[string]any)
	if !ok || !isMap {
		return left, ok
	}
	delete(fields, "type")
	if len(fields) == 0 {
		return nil, false
	}
	fields["type"] = discriminator.Type
	return fields, true
}

// rawString decodes a JSON string; ok is false for any other JSON type.
func rawString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

// storedNonEmpty matches a typed home that stores the string verbatim when non-empty.
func storedNonEmpty(raw json.RawMessage) bool {
	value, ok := rawString(raw)
	return ok && value != ""
}

// storedTrimmed matches a typed home that trims before storing (ids read through
// strings.TrimSpace / normalize.ObservedString): only an already-trimmed non-empty
// value is retained exactly.
func storedTrimmed(raw json.RawMessage) bool {
	value, ok := rawString(raw)
	return ok && value != "" && strings.TrimSpace(value) == value
}

// storedNonBlank matches a typed home that stores the value verbatim unless it is
// whitespace-only: fallbackString (version), the text/thinking content shapers, and
// the tool_use id / tool_result tool_use_id pairing key.
func storedNonBlank(raw json.RawMessage) bool {
	value, ok := rawString(raw)
	return ok && strings.TrimSpace(value) != ""
}

// alwaysStored marks a body whose typed home retains it whole (a tool input,
// a tool_result body, the record-scoped toolUseResult — all on the Operation).
func alwaysStored(json.RawMessage) bool { return true }

// storedTimestamp matches occurred_at, which is the RFC3339 timestamp in UTC; a raw
// value that does not round-trip byte-for-byte (an offset, a different precision)
// stays in the residual.
func storedTimestamp(raw json.RawMessage) bool {
	value, ok := rawString(raw)
	if !ok {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.UTC().Format(time.RFC3339Nano) == value
}

// storedInteger matches a typed int64 home (token counts, apiBlockIndex) holding the
// exact decimal the wire carried.
func storedInteger(raw json.RawMessage) bool {
	// json.Number also decodes a quoted "4"; the typed home would store it as an
	// int64, losing that it was a string, so only a bare JSON number is typed.
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] == '"' {
		return false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return false
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	return err == nil && parsed >= 0 && strconv.FormatInt(parsed, 10) == number.String()
}

// storedBool matches a typed bool home.
func storedBool(raw json.RawMessage) bool {
	var value bool
	return json.Unmarshal(raw, &value) == nil
}

// transcriptUsageRules are the message.usage counts promoted to canonical token
// attributes (addTokenCounts) and cache_usage_extra (cacheExtraTokens).
var transcriptUsageRules = objectRules{
	"input_tokens":                scalarRule(storedInteger),
	"output_tokens":               scalarRule(storedInteger),
	"cache_read_input_tokens":     scalarRule(storedInteger),
	"cache_creation_input_tokens": scalarRule(storedInteger),
	"output_tokens_details":       objectRules{"thinking_tokens": scalarRule(storedInteger)},
	"cache_creation": objectRules{
		"ephemeral_1h_input_tokens": scalarRule(storedInteger),
		"ephemeral_5m_input_tokens": scalarRule(storedInteger),
	},
}

// transcriptEnvelopeRules are the record fields with a typed home on every
// user_message / assistant_message event: the event identity (type → event_type,
// sessionId → session_id, timestamp → occurred_at, version → source_version),
// correlation (uuid, parentUuid, promptId), and transcriptEnvelope.
var transcriptEnvelopeRules = map[string]residualNode{
	"type":           scalarRule(storedNonEmpty),
	"uuid":           scalarRule(storedTrimmed),
	"parentUuid":     scalarRule(storedTrimmed),
	"promptId":       scalarRule(storedTrimmed),
	"sessionId":      scalarRule(storedTrimmed),
	"timestamp":      scalarRule(storedTimestamp),
	"version":        scalarRule(storedNonBlank),
	"cwd":            scalarRule(storedNonEmpty),
	"gitBranch":      scalarRule(storedNonEmpty),
	"entrypoint":     scalarRule(storedNonEmpty),
	"userType":       scalarRule(storedNonEmpty),
	"requestId":      scalarRule(storedNonEmpty),
	"effort":         scalarRule(storedNonEmpty),
	"permissionMode": scalarRule(storedNonEmpty),
	"promptSource":   scalarRule(storedNonEmpty),
	"turnOrigin":     scalarRule(storedNonEmpty),
	"apiBlockIndex":  scalarRule(storedInteger),
	"isSidechain":    scalarRule(storedBool),
	// toolUseResult rides on the paired tool Operation (#173, Site 3).
	"toolUseResult": scalarRule(alwaysStored),
}

// transcriptToolResultBlock is a user tool_result block, whose body and outcome ride
// on the paired tool Operation.
var transcriptToolResultBlock = objectRules{
	"tool_use_id": scalarRule(storedNonBlank),
	"content":     scalarRule(alwaysStored),
	"is_error":    scalarRule(storedBool),
}

// transcriptAssistantRules reduce an assistant record: its response/thinking text
// and tool_use invocations have typed homes, as do model/id/stop_reason and usage.
var transcriptAssistantRules = transcriptRecordRules(objectRules{
	"content": contentRules{blocks: map[string]objectRules{
		"text":     {"text": scalarRule(storedNonBlank)},
		"thinking": {"thinking": scalarRule(storedNonBlank)},
		"tool_use": {
			"id":    scalarRule(storedNonBlank),
			"name":  scalarRule(storedNonEmpty),
			"input": scalarRule(alwaysStored),
		},
	}},
	"model": scalarRule(storedTrimmed),
	"usage": transcriptUsageRules,
})

// transcriptUserRules reduce a content-bearing user record: its prompt text and any
// tool_result blocks have typed homes.
var transcriptUserRules = transcriptRecordRules(objectRules{
	"content": contentRules{stringTyped: true, blocks: map[string]objectRules{
		"text":        {"text": scalarRule(storedNonBlank)},
		"tool_result": transcriptToolResultBlock,
	}},
})

// transcriptResultRecordRules reduce a tool_result-only user record carried on the
// paired tool call's event (transcript_result): only the tool_result bodies and the
// record-scoped toolUseResult have a typed home there (the Operation), so every
// envelope field is echoed raw.
var transcriptResultRecordRules = objectRules{
	"toolUseResult": transcriptEnvelopeRules["toolUseResult"],
	"message": objectRules{
		"content": contentRules{blocks: map[string]objectRules{"tool_result": transcriptToolResultBlock}},
	},
}

// transcriptRecordRules assembles the record rules shared by the assistant and user
// shapers: the envelope rules, plus the record type's message rules extended with
// the message id / stop_reason that transcriptEnvelope stores for both.
func transcriptRecordRules(message objectRules) objectRules {
	message["id"] = scalarRule(storedNonEmpty)
	message["stop_reason"] = scalarRule(storedNonEmpty)
	rules := objectRules{"message": message}
	for key, rule := range transcriptEnvelopeRules {
		rules[key] = rule
	}
	return rules
}

// transcriptRecordResidual returns the raw fields of a transcript line that the
// rules leave without a typed home, or nil when every field is typed.
func transcriptRecordResidual(line []byte, rules objectRules) map[string]any {
	left, ok := rules.residual(line)
	if !ok {
		return nil
	}
	fields, _ := left.(map[string]any)
	return fields
}
