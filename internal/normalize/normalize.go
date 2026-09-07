// Package normalize holds provider-independent helpers shared by the per-tool
// adapters (internal/normalize/codex, internal/normalize/claude): decoded-JSON
// field extraction, honest token parsing, and canonical dedup/ordering. Keeping
// these in one place avoids duplicating identical logic across adapters while
// leaving provider-specific mapping in each adapter.
package normalize

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// RequiredString returns a non-empty trimmed string at key, or an error naming
// the key (never its value).
func RequiredString(value map[string]any, key string) (string, error) {
	stringValue, ok := value[key].(string)
	if !ok || strings.TrimSpace(stringValue) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return stringValue, nil
}

// OptionalString returns a trimmed non-empty string at key, or nil when absent,
// non-string, or whitespace-only.
func OptionalString(value map[string]any, key string) *string {
	stringValue, ok := value[key].(string)
	if !ok {
		return nil
	}
	stringValue = strings.TrimSpace(stringValue)
	if stringValue == "" {
		return nil
	}
	return &stringValue
}

// ObservedString returns the trimmed string value and whether a non-empty value
// was actually observed. Absent, non-string, or whitespace-only values yield
// ("unknown", false) so a blank value is never mistaken for a real one.
func ObservedString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "unknown", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "unknown", false
	}
	return text, true
}

// ProviderNativeSessionID returns the local-edition canonical session identity:
// a stable provider prefix plus the provider-native session/conversation ID.
// If a malformed payload carries a secret-like value despite the upstream
// sanitizer, fall back to a fingerprint so the sensitive value is not retained.
func ProviderNativeSessionID(prefix, value string, fingerprint func([]byte) string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return prefix + "unknown"
	}
	if looksSecretLikeIdentifier(value) {
		if fingerprint == nil {
			return prefix + "redacted"
		}
		return prefix + "redacted:" + fingerprint([]byte(value))
	}
	return prefix + value
}

func looksSecretLikeIdentifier(value string) bool {
	normalized := strings.ToLower(value)
	for _, marker := range []string{
		"api_key=",
		"apikey=",
		"authorization:",
		"bearer ",
		"password=",
		"secret=",
		"token=",
		"-----begin private key-----",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return strings.HasPrefix(normalized, "sk-") || value == "[REDACTED]"
}

// OptionalTokenCount parses a token attribute into a *int64. Decoded JSON
// encodes an integer string as a string and a number as float64; both are
// accepted. An absent, unparseable, negative, non-integral, or out-of-range
// value yields nil — never a fabricated or silently truncated count — so a
// genuine absence stays distinguishable from a real 0.
func OptionalTokenCount(value any) *int64 {
	var count int64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return nil
		}
		count = parsed
	case float64:
		if math.Trunc(typed) != typed || typed < math.MinInt64 || typed >= math.MaxInt64 {
			return nil
		}
		count = int64(typed)
	default:
		return nil
	}
	if count < 0 {
		return nil
	}
	return &count
}

// UnknownFields returns the entries of value whose keys are not in knownKeys, so
// safe fields outside a mapped set survive verbatim as evidence.
func UnknownFields(value map[string]any, knownKeys ...string) map[string]any {
	known := make(map[string]struct{}, len(knownKeys))
	for _, key := range knownKeys {
		known[key] = struct{}{}
	}
	unknown := make(map[string]any)
	for key, fieldValue := range value {
		if _, found := known[key]; !found {
			unknown[key] = fieldValue
		}
	}
	return unknown
}

// InteractionProvenance is observed when the model and at least one token count
// were directly observed; otherwise the record is explicitly unknown.
func InteractionProvenance(modelObserved bool, inputTokens, outputTokens *int64) canonical.Provenance {
	if modelObserved && (inputTokens != nil || outputTokens != nil) {
		return canonical.ProvenanceObserved
	}
	return canonical.ProvenanceUnknown
}

// CorrelateEvents returns events ordered by observed time plus stable
// identifiers, with duplicate event IDs collapsed.
func CorrelateEvents(events []canonical.Event) []canonical.Event {
	ordered := append([]canonical.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return eventLess(ordered[i], ordered[j]) })
	seen := make(map[string]struct{}, len(ordered))
	correlated := make([]canonical.Event, 0, len(ordered))
	for _, event := range ordered {
		if _, ok := seen[event.EventID]; ok {
			continue
		}
		seen[event.EventID] = struct{}{}
		correlated = append(correlated, event)
	}
	return correlated
}

func eventLess(left, right canonical.Event) bool {
	if !left.OccurredAt.Equal(right.OccurredAt) {
		return left.OccurredAt.Before(right.OccurredAt)
	}
	if left.EventID != right.EventID {
		return left.EventID < right.EventID
	}
	if left.EventType != right.EventType {
		return left.EventType < right.EventType
	}
	return left.ReceivedAt.Before(right.ReceivedAt)
}

// CorrelateModelInteractions returns records ordered by start time plus stable
// identifiers, with duplicate request IDs collapsed.
func CorrelateModelInteractions(records []canonical.ModelInteraction) []canonical.ModelInteraction {
	ordered := append([]canonical.ModelInteraction(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool { return modelInteractionLess(ordered[i], ordered[j]) })
	seen := make(map[string]struct{}, len(ordered))
	correlated := make([]canonical.ModelInteraction, 0, len(ordered))
	for _, record := range ordered {
		if _, ok := seen[record.RequestID]; ok {
			continue
		}
		seen[record.RequestID] = struct{}{}
		correlated = append(correlated, record)
	}
	return correlated
}

func modelInteractionLess(left, right canonical.ModelInteraction) bool {
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	if left.RequestID != right.RequestID {
		return left.RequestID < right.RequestID
	}
	return left.CompletedAt.Before(right.CompletedAt)
}
