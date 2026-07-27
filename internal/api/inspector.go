package api

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/wayne/telemetryiq/internal/privacy"
)

type sanitizedInspector struct {
	sanitizer  *privacy.Sanitizer
	mu         sync.RWMutex
	value      map[string]any
	provenance []privacy.Provenance
}

func newSanitizedInspector(sanitizer *privacy.Sanitizer) *sanitizedInspector {
	return &sanitizedInspector{sanitizer: sanitizer}
}

func (i *sanitizedInspector) capture(payload map[string]json.RawMessage) {
	raw := make(map[string]any, len(payload))
	for key, value := range payload {
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			raw[key] = decoded
		}
	}
	result := i.sanitizer.Sanitize(raw)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.value, i.provenance = result.Value, result.Provenance
}

func (i *sanitizedInspector) handler(w http.ResponseWriter, _ *http.Request) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"payload": i.value, "provenance": i.provenance})
}
