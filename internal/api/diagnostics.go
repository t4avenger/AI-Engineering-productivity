package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/storage"
)

const diagnosticSchemaVersion = "0.1.0"

type diagnosticAPI struct {
	reader   storage.DiagnosticSummaryReader
	counters *otlpHTTPIngest
}

type diagnosticDocument struct {
	SchemaVersion string                    `json:"schema_version"`
	GeneratedAt   string                    `json:"generated_at"`
	Service       string                    `json:"service"`
	Ingest        ingestCountersResponse    `json:"ingest"`
	Storage       storage.DiagnosticSummary `json:"storage"`
}

type diagnosticResponse struct {
	Data diagnosticDocument `json:"data"`
}

func (a diagnosticAPI) preview(w http.ResponseWriter, r *http.Request) {
	a.write(w, r, false)
}

func (a diagnosticAPI) export(w http.ResponseWriter, r *http.Request) {
	a.write(w, r, true)
}

func (a diagnosticAPI) write(w http.ResponseWriter, r *http.Request, attachment bool) {
	if a.reader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "diagnostics_unavailable", "diagnostics are unavailable")
		return
	}
	summary, err := a.reader.DiagnosticSummary(r.Context())
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "diagnostics_failed", "unable to generate diagnostics")
		return
	}
	document := diagnosticDocument{
		SchemaVersion: diagnosticSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Service:       "telemetryiq-daemon",
		Storage:       summary,
	}
	if a.counters != nil {
		document.Ingest = ingestCountersResponse{
			AcceptedPayloads: a.counters.accepted.Load(),
			RejectedPayloads: a.counters.rejected.Load(),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if attachment {
		w.Header().Set("Content-Disposition", `attachment; filename="telemetryiq-diagnostics.json"`)
	}
	if err := json.NewEncoder(w).Encode(diagnosticResponse{Data: document}); err != nil {
		return
	}
}
