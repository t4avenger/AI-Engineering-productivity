package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func FuzzNormalizeMetrics(f *testing.F) {
	f.Add(otlpPayloadSeed(f, "cursor-otel-0.1.0-token-usage-metrics.json"))
	f.Add([]byte(`{"resourceMetrics":[]}`))
	f.Add([]byte("not json"))
	f.Add([]byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeMetrics":[{"scope":{"name":"cursor.telemetry"},"metrics":[{"name":"cursor.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"cursor.token.type","value":{"stringValue":"input"}}],"asDouble":1}]}}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeMetrics(data, time.Unix(0, 1790000000000000001).UTC())
	})
}

func FuzzNormalizeLogs(f *testing.F) {
	f.Add(otlpPayloadSeed(f, "cursor-otel-0.1.0-api-request-logs.json"))
	f.Add([]byte(`{"resourceLogs":[]}`))
	f.Add([]byte("not json"))
	f.Add([]byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeLogs":[{"scope":{"name":"cursor.telemetry"},"logRecords":[{"body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.api.request.input_tokens","value":{"intValue":"1"}}]}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeLogs(data, time.Unix(0, 1790000000000000001).UTC())
	})
}

func otlpPayloadSeed(f *testing.F, name string) []byte {
	f.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		f.Fatal("locate test source")
	}
	wrapper, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "cursor", "observed-sanitised", name))
	if err != nil {
		f.Fatalf("read fuzz seed: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(wrapper, &document); err != nil {
		f.Fatalf("decode fixture: %v", err)
	}
	raw, err := json.Marshal(document["payload"])
	if err != nil {
		f.Fatalf("marshal payload: %v", err)
	}
	return raw
}
