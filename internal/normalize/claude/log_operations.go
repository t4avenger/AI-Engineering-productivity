package claude

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ExtractLogOperations maps raw Claude Code OTLP/HTTP logs into canonical
// Operation records. It keeps NormalizeLogs as the event-only adapter while
// reusing the reviewed sample-event operation mapper after the same wire
// reduction used by NormalizeLogs.
func ExtractLogOperations(data []byte, _ time.Time) ([]canonical.Operation, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Claude OTLP logs for operations: %w", err)
	}
	var records []canonical.Operation
	for _, resource := range payload.ResourceLogs {
		resourceRecords, err := resourceLogOperations(resource, len(records))
		if err != nil {
			return nil, err
		}
		records = append(records, resourceRecords...)
	}
	if len(records) == 0 {
		return nil, ErrUnsupportedLogs
	}
	return normalize.CorrelateOperations(records), nil
}

func resourceLogOperations(resource resourceLog, indexBase int) ([]canonical.Operation, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs["service.name"].(string); service != claudeLogService {
		return nil, nil
	}
	var records []canonical.Operation
	for _, scope := range resource.ScopeLogs {
		scopeRecords, err := scopeLogOperations(scope, indexBase+len(records))
		if err != nil {
			return nil, err
		}
		records = append(records, scopeRecords...)
	}
	return records, nil
}

func scopeLogOperations(scope scopeLog, indexBase int) ([]canonical.Operation, error) {
	var records []canonical.Operation
	for _, record := range scope.LogRecords {
		operation, ok, err := sampleOperation(indexBase+len(records), sampleEventFromRecord(record))
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, operation)
		}
	}
	return records, nil
}
