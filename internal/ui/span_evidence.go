package ui

import (
	"strconv"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"

	"github.com/wayne/telemetryiq/internal/spans"
)

const sessionSpanEvidenceLimit = 50

type spanEvidenceRow struct {
	TraceID            string
	SpanID             string
	ParentID           string
	ParentAvailability string
	Name               string
	Duration           string
	IntervalState      string
	Status             string
	SourceEvents       string
}

func spanEvidenceRows(events []canonical.Event) ([]spanEvidenceRow, bool) {
	records := spans.Project(events)
	page, next := spans.Page(records, sessionSpanEvidenceLimit, "", "")
	rows := make([]spanEvidenceRow, 0, len(page))
	for _, record := range page {
		row := spanEvidenceRow{
			TraceID: record.TraceID, SpanID: record.SpanID, ParentAvailability: record.ParentAvailability,
			IntervalState: record.IntervalAvailability, SourceEvents: strings.Join(record.SourceEventIDs, ", "),
		}
		if record.ParentSpanID != nil {
			row.ParentID = *record.ParentSpanID
		}
		if record.Name != nil {
			row.Name = *record.Name
		}
		if record.DurationMs != nil {
			row.Duration = strconv.FormatInt(*record.DurationMs, 10) + " ms"
		}
		if record.StatusCode != nil {
			row.Status = strconv.FormatInt(*record.StatusCode, 10)
		}
		rows = append(rows, row)
	}
	return rows, next != nil
}
