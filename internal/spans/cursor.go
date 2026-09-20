package spans

// Contains reports whether a cursor identity still exists in the retained
// projection. An absent identity is a stale cursor, not the first page.
func Contains(records []Record, traceID, spanID string) bool {
	for _, record := range records {
		if record.TraceID == traceID && record.SpanID == spanID {
			return true
		}
	}
	return false
}
