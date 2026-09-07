package cursor

import (
	"reflect"
	"testing"
)

func TestCursorGoldenFixtures(t *testing.T) {
	cases := []struct {
		name          string
		fixture       string
		eventsGolden  string
		recordsGolden string
	}{
		{
			name:          "print_json",
			fixture:       "cursor-agent-2026.05.16-0338208-print-result.json",
			eventsGolden:  "cursor-agent-2026.05.16-0338208-print-result.events.json",
			recordsGolden: "cursor-agent-2026.05.16-0338208-print-result.records.json",
		},
		{
			name:          "stream_json",
			fixture:       "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json",
			eventsGolden:  "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.events.json",
			recordsGolden: "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.records.json",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			input := readFixture(t, tc.fixture)

			eventsFirst := normalizeDeterministic(t, input)
			if updateGolden() {
				writeGolden(t, tc.eventsGolden, eventsFirst)
			}
			assertMatchesGolden(t, tc.eventsGolden, eventsFirst)

			recordsFirst := extractDeterministic(t, input)
			if updateGolden() {
				writeGolden(t, tc.recordsGolden, recordsFirst)
			}
			assertMatchesGolden(t, tc.recordsGolden, recordsFirst)
		})
	}
}

func normalizeDeterministic(t *testing.T, input []byte) any {
	t.Helper()
	first, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	return first
}

func extractDeterministic(t *testing.T, input []byte) any {
	t.Helper()
	first, err := ExtractModelInteractions(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}
	second, err := ExtractModelInteractions(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction must be deterministic")
	}
	return first
}
