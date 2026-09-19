package canonical

import "strings"

// PRLinkAvailability reports whether session-level pull/merge-request evidence
// is observed, ambiguous, or absent without selecting among conflicting URLs.
func PRLinkAvailability(attributes map[string]any) string {
	if link, ok := attributes["pr_link"].(string); ok && strings.TrimSpace(link) != "" {
		return "observed"
	}
	if prLinkCandidateCount(attributes["pr_link_candidate_count"]) > 1 {
		return "partial"
	}
	return "unavailable"
}

func prLinkCandidateCount(value any) int64 {
	switch count := value.(type) {
	case int:
		return int64(count)
	case int32:
		return int64(count)
	case int64:
		return count
	case float64:
		return int64(count)
	default:
		return 0
	}
}
