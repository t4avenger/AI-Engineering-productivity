package governance

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func FuzzPromptKeywordMatch(f *testing.F) {
	f.Add("literal", "secret", "has secret")
	f.Add("regex", "(?i)token", "TOKEN")
	f.Add("regex", `(a+)+$`, strings.Repeat("a", 64)+"b")
	f.Fuzz(func(t *testing.T, kind, pattern, text string) {
		if len(pattern) > 1024 || len(text) > 8192 {
			return
		}
		var compiled *regexp.Regexp
		if kind == "regex" {
			var err error
			compiled, err = regexp.Compile(pattern)
			if err != nil {
				if matchPromptKeyword(kind, pattern, nil, text) {
					t.Fatal("invalid pattern matched")
				}
				return
			}
		}
		_ = matchPromptKeyword(kind, pattern, compiled, text)
	})
}

// FuzzPromptKeywordsFromEvents feeds arbitrary prompt text, session ids, and
// wrongly typed correlation values through the dual-source join (#259). The
// scan must never panic and must be deterministic for the same input.
func FuzzPromptKeywordsFromEvents(f *testing.F) {
	f.Add("u1", "u1", "session-1", "has Secret", "Secret", true)
	f.Add("", "u1", " ", "<redacted>", "Secret", false)
	f.Add("u1", "u2", "session-1", "Secret", "", true)
	f.Fuzz(func(t *testing.T, otlpUUID, transcriptUUID, session, text, rule string, typedCorrelation bool) {
		if len(text) > 8192 || len(rule) > 256 {
			return
		}
		otlp := promptEvent("p", "available", text)
		transcript := transcriptPromptEvent("t", transcriptUUID, text)
		otlp.SessionID, transcript.SessionID = session, session
		if typedCorrelation {
			otlp = withMessageUUID(otlp, otlpUUID)
		} else {
			otlp.ProviderExtensions["correlation"] = otlpUUID
			transcript.ProviderExtensions["correlation"] = map[string]any{"uuid": len(transcriptUUID)}
		}
		events := []canonical.Event{otlp, transcript}
		rules := []config.PromptKeyword{{ID: "r", Label: "R", Group: "custom", Enabled: true, Kind: "literal", Value: rule}}
		first := PromptKeywordsFromEvents(events, rules)
		if !reflect.DeepEqual(first, PromptKeywordsFromEvents(events, rules)) {
			t.Fatal("prompt keyword scan is not deterministic")
		}
		if len(first.Findings) > 2 {
			t.Fatalf("two prompt copies produced %d findings", len(first.Findings))
		}
	})
}
