package api

import (
	"encoding/json"
	"net/http"
	"sync"
)

// ingestInspector is a development-only view of the last received ingest
// payload. It captures the payload verbatim — no ingest-time hiding is applied
// (epic #87), so the inspector shows the raw data exactly as it reaches the
// normalizers and storage.
type ingestInspector struct {
	mu    sync.RWMutex
	value map[string]any
}

func newIngestInspector() *ingestInspector {
	return &ingestInspector{}
}

func (i *ingestInspector) captureValue(raw map[string]any) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.value = raw
}

func (i *ingestInspector) capture(payload map[string]json.RawMessage) {
	raw := make(map[string]any, len(payload))
	for key, value := range payload {
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			raw[key] = decoded
		}
	}
	i.captureValue(raw)
}

func (i *ingestInspector) handler(w http.ResponseWriter, _ *http.Request) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"payload": i.value})
}
