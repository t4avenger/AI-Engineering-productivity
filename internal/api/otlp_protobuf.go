package api

import (
	"encoding/json"
	"fmt"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	otlpContentTypeJSON     = "application/json"
	otlpContentTypeProtobuf = "application/x-protobuf"
)

// decodeOTLPProtobuf unmarshals an OTLP/HTTP Export*ServiceRequest body.
// ExportLogsServiceRequest / ExportMetricsServiceRequest are wire-compatible
// with LogsData / MetricsData (field 1 = repeated resource_*); we decode via
// the data messages to avoid pulling collector gRPC stubs. Cursor Enterprise
// OpenTelemetry Export sends this encoding only (#129).
func decodeOTLPProtobuf(body []byte, resourceField string) (map[string]json.RawMessage, error) {
	var message proto.Message
	switch resourceField {
	case "resourceLogs":
		message = &logspb.LogsData{}
	case "resourceMetrics":
		message = &metricspb.MetricsData{}
	default:
		return nil, fmt.Errorf("protobuf ingest is not supported for %s", resourceField)
	}
	if err := proto.Unmarshal(body, message); err != nil {
		return nil, fmt.Errorf("unmarshal protobuf: %w", err)
	}

	encoded, err := protojson.MarshalOptions{
		UseProtoNames:   false,
		EmitUnpopulated: false,
	}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("marshal protojson: %w", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("decode protojson: %w", err)
	}
	return payload, nil
}

func otlpAcceptsProtobuf(resourceField string) bool {
	return resourceField == "resourceLogs" || resourceField == "resourceMetrics"
}
