package canonical

import "testing"

func TestPRLinkAvailability(t *testing.T) {
	tests := []struct {
		name       string
		attributes map[string]any
		want       string
	}{
		{name: "observed", attributes: map[string]any{"pr_link": "https://gitlab.example.test/group/project/-/merge_requests/12"}, want: "observed"},
		{name: "ambiguous persisted session", attributes: map[string]any{"pr_link_candidate_count": float64(2)}, want: "partial"},
		{name: "unavailable", attributes: map[string]any{}, want: "unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PRLinkAvailability(tc.attributes); got != tc.want {
				t.Fatalf("availability = %q, want %q", got, tc.want)
			}
		})
	}
}
