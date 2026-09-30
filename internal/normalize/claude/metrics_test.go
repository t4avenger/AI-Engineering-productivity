package claude

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// metricEventIDStability freezes the metric event IDs each fixture produced
// BEFORE #173 switched provider_extensions.metric_attributes/resource from the
// safe allow-list to raw retention. Retaining attributes raw and routing identity
// into provider_extensions.environment must not perturb the datapoint identity
// hash — safeMetricAttributes and resourceIdentityKey still feed it unchanged — so
// a re-ingested historical datapoint still deduplicates to the same event under
// CorrelateEvents (and the event_id primary key in storage) rather than fanning
// out a second copy. These literals are captured from the pre-#173 goldens;
// regenerating a golden must never change them. If they must change, that is an
// explicit versioned-ID migration, not an incidental golden regen.
var metricEventIDStability = map[string]struct {
	receivedAt time.Time
	eventIDs   []string
}{
	"claude-code-2.1.268-token-usage-metrics.json": {
		receivedAt: time.Date(2026, 9, 11, 8, 50, 31, 0, time.UTC),
		eventIDs: []string{
			"claude-code:token:1bc3c2f9fdb75372522483f3f56ab58d8589f8301fbfd85bd80b9d0c6631c00a",
			"claude-code:token:205cdb21103c9187692f40705e019d699e2426dcf914aaae6309bd0e41d79fda",
			"claude-code:token:40b026bfa288dd829076fe1900dd202869693a80931f4157d0b9d63ae484d65b",
			"claude-code:token:47c7fecee2ed178767beda13c424b2b6211d9f0e2c3d3bf239730f82f49770ad",
		},
	},
	"claude-code-2.1.268-cost-attribution-metrics.json": {
		receivedAt: time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC),
		eventIDs: []string{
			"claude-code:token:8387de0ecdb7c0f95b5dc96aeef541aa238fc87e4e093d72038188982c880c51",
			"claude-code:token:a2956243a6ad9a15cc5c04a1a9fe5090868dcb77b186b971a30a71e29bebf137",
			"claude-code:cost:d1f120f0dcb232badd8197e5831c3af06152a72049e37655b66d39d1b1803734",
			"claude-code:cost:a3f85239fbbfa0d1952a89f1027926e87209cc096b98d8dcd6285825c19a6f06",
		},
	},
	"claude-code-2.1.268-code-output-metrics.json": {
		receivedAt: time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC),
		eventIDs: []string{
			"claude-code:count:0318b1c04760fb96a8b5f3e8e97dd71b879206a500ab16b159e98a3a50201a4b",
			"claude-code:count:fb7d95c064a0e4169b84e6216a4a35c1bec568b809264eab0aac6281c5ad0ed8",
			"claude-code:count:a39ddf857a5b96f6b4721cc44e363f6a0cdc3991095a8dddf4ee4173b936f7d9",
			"claude-code:count:2491d2aa4803e024363e7448429db192bc70ff59714316a9771ecc9dfc781259",
		},
	},
	"claude-code-2.1.268-engagement-metrics.json": {
		receivedAt: time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC),
		eventIDs: []string{
			"claude-code:count:41b045616d32e57c34be87364f21a79243d319cacebfd82b58ee0d3221746b45",
			"claude-code:count:590fcbcc3631acc3585b5faeb0e0f875fa9bb948270a02cfaa2bacbc40faf254",
			"claude-code:count:736a620b6203347958cba660c4df570cf079778645967bdaf92045ae741aba80",
			"claude-code:count:5326ecb2b84f2bf8a8be2cd0cc96cb88a863ff791a60ac1e3aeba530df1872d9",
			"claude-code:count:db3f376c4b81b0baccb22a8d1381ba2017ca67de640afa9481864c9a6b06415f",
			"claude-code:count:8fc134bf3cf44c08f583d62d4ce4a0eca19fc2c8747f3e7f9a21abeff453f909",
		},
	},
}

// TestNormalizeMetricsEventIDsStable proves the #173 raw-retention change keeps
// every metric event ID byte-identical to the pre-change goldens (see
// metricEventIDStability), so re-ingesting a historical datapoint deduplicates
// rather than producing a second event. It compares the produced ID set against
// frozen literals, not the (regeneratable) golden files, so a golden regen cannot
// mask an ID drift.
func TestNormalizeMetricsEventIDsStable(t *testing.T) {
	for fixture, want := range metricEventIDStability {
		t.Run(fixture, func(t *testing.T) {
			payload := metricsFixturePayload(t, fixture)
			events, err := NormalizeMetrics(payload, want.receivedAt)
			if err != nil {
				t.Fatalf("NormalizeMetrics: %v", err)
			}
			got := make([]string, 0, len(events))
			for _, event := range events {
				got = append(got, event.EventID)
			}
			sort.Strings(got)
			wantIDs := append([]string(nil), want.eventIDs...)
			sort.Strings(wantIDs)
			if !reflect.DeepEqual(got, wantIDs) {
				t.Fatalf("metric event IDs changed (must stay byte-identical to pre-#173 goldens)\n got: %#v\nwant: %#v", got, wantIDs)
			}
		})
	}
}

// metricsFixturePayload extracts the OTLP payload from the fixture wrapper so it
// is replayed exactly as the /v1/metrics route would hand it to the adapter.
func metricsFixturePayload(t *testing.T, name string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readFixture(t, name), &document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func TestNormalizeMetricsTokenUsageGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-token-usage-metrics.json")
	receivedAt := time.Date(2026, 9, 11, 8, 50, 31, 0, time.UTC)

	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeTokenUsageEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-token-usage-metrics.events.json", first)
}

func assertClaudeTokenUsageEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4 token categories", len(events))
	}
	for key, want := range map[string]int64{
		"input_token_count":             10,
		"output_token_count":            42,
		"cached_input_token_count":      0,
		"cache_write_input_token_count": 18856,
	} {
		assertClaudeTokenAttribute(t, events, key, want)
	}
	for _, event := range events {
		if event.EventType != tokenUsageMetric {
			t.Fatalf("event type = %q", event.EventType)
		}
		if event.Provider != provider || event.Tool != tool {
			t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
		}
		if event.Attributes["model"] != "claude-haiku-4-5-20251001" {
			t.Fatalf("model = %#v", event.Attributes["model"])
		}
		if event.SessionID != "claude-code:00000000-0000-4000-8000-000000000001" {
			t.Fatalf("session id = %q, want raw provider-native session id", event.SessionID)
		}
	}
}

func assertClaudeTokenAttribute(t *testing.T, events []canonical.Event, key string, want int64) {
	t.Helper()
	for _, event := range events {
		got, ok := event.Attributes[key]
		if !ok {
			continue
		}
		if got != want {
			t.Fatalf("%s = %#v, want %d", key, got, want)
		}
		return
	}
	t.Fatalf("missing token attribute %s in %#v", key, events)
}

func TestNormalizeMetricsCostAttributionGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-cost-attribution-metrics.json")
	receivedAt := time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC)

	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeCostAttributionEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-cost-attribution-metrics.events.json", first)
}

// assertClaudeCostAttributionEvents proves the M10 outcome: the attribution dims
// survive into provider_extensions.metric_attributes on both token.usage and
// cost.usage events, the provider-reported USD cost is carried under
// provider_cost, and — because cost.usage is captured on the same resource — the
// token events no longer declare provider_cost unavailable.
func assertClaudeCostAttributionEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	var tokenEvents, costEvents int
	for _, event := range events {
		switch event.EventType {
		case tokenUsageMetric:
			tokenEvents++
			assertProviderCostAvailable(t, event)
		case costUsageMetric:
			costEvents++
			assertPositiveProviderCost(t, event)
		default:
			t.Fatalf("unexpected event type %q", event.EventType)
		}
		assertMetricIdentityRetained(t, event)
	}
	if tokenEvents != 2 || costEvents != 2 {
		t.Fatalf("event mix = %d token / %d cost, want 2/2", tokenEvents, costEvents)
	}
	assertAttribution(t, events, "code-reviewer", map[string]any{"skill.name": "code-reviewer", "agent.name": "general-purpose"})
	assertAttribution(t, events, "review-suite", map[string]any{"mcp_server.name": "github", "mcp_tool.name": "create_issue", "plugin.name": "review-suite", "marketplace.name": "acme-marketplace"})
}

// assertProviderCostAvailable proves a token.usage event does not mark
// provider_cost unavailable when a sibling cost.usage datapoint is captured on
// the same resource.
func assertProviderCostAvailable(t *testing.T, event canonical.Event) {
	t.Helper()
	for _, field := range event.Attributes["unavailable_fields"].([]string) {
		if field == "provider_cost" {
			t.Fatalf("token event must not mark provider_cost unavailable when cost.usage is captured: %#v", event.Attributes)
		}
	}
}

// assertPositiveProviderCost proves a cost.usage event carries the provider USD
// amount under provider_cost.
func assertPositiveProviderCost(t *testing.T, event canonical.Event) {
	t.Helper()
	cost, ok := event.Attributes["provider_cost"].(float64)
	if !ok || cost <= 0 {
		t.Fatalf("cost event must carry a positive provider_cost, got %#v", event.Attributes["provider_cost"])
	}
}

// metricIdentityRouting maps the raw datapoint identity keys #173 now retains to
// their provider_extensions.environment canonical names, so one table drives the
// retention+routing assertion across every metric golden instead of repeating it.
var metricIdentityRouting = map[string]string{
	"user.id":         "user_id",
	"user.email":      "user_email",
	"organization.id": "organization_id",
	"terminal.type":   "terminal_type",
}

// assertMetricIdentityRetained proves #173's raw-capture outcome on one event:
// the operator identity the pre-#173 allow-list dropped is now retained raw in
// provider_extensions.metric_attributes and, where it has an environment home, is
// routed into provider_extensions.environment too (so actor_id derives from it).
// Nothing is amputated at the local-only ingest boundary (epic #87); what to hide
// is a downstream visibility layer. Every fixture datapoint carries user.id, so
// that anchors the retention check and actor_id derivation.
func assertMetricIdentityRetained(t *testing.T, event canonical.Event) {
	t.Helper()
	attrs := event.ProviderExtensions["metric_attributes"].(map[string]any)
	userID, ok := attrs["user.id"].(string)
	if !ok {
		t.Fatalf("user.id must be retained raw in metric_attributes (nothing dropped at ingest): %#v", attrs)
	}
	if event.ActorID != nativeSessionPrefix+userID {
		t.Fatalf("actor_id = %q, want it derived from retained user.id %q", event.ActorID, userID)
	}
	environment, _ := event.ProviderExtensions["environment"].(map[string]any)
	for rawKey, envKey := range metricIdentityRouting {
		value, present := attrs[rawKey]
		if !present {
			continue
		}
		if environment[envKey] != value {
			t.Fatalf("identity %q retained raw must route to environment[%q]: attrs=%#v env=%#v", rawKey, envKey, attrs, environment)
		}
	}
}

// assertAttribution finds an event whose metric_attributes carry the marker
// value and asserts every expected attribution dim is present and equal.
func assertAttribution(t *testing.T, events []canonical.Event, marker string, want map[string]any) {
	t.Helper()
	for _, event := range events {
		attrs := event.ProviderExtensions["metric_attributes"].(map[string]any)
		matched := false
		for _, value := range attrs {
			if value == marker {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for key, value := range want {
			if attrs[key] != value {
				t.Fatalf("attribution dim %q = %#v, want %#v (event %s)", key, attrs[key], value, event.EventType)
			}
		}
		return
	}
	t.Fatalf("no event carried attribution marker %q", marker)
}

func TestNormalizeMetricsCodeOutputGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-code-output-metrics.json")
	receivedAt := time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC)

	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeCodeOutputEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-code-output-metrics.events.json", first)
}

// assertClaudeCodeOutputEvents proves the M11 outcome: the three code-output
// counters land on their canonical keys (lines_added/lines_removed on
// lines_of_code, plus commit/pull_request counts), model rides only lines_of_code,
// commit/pull_request carry no model, and the lines_of_code attribution dims
// survive into provider_extensions.metric_attributes so produced code is
// attributable to the skill/sub-agent that produced it — with no identity leak.
func assertClaudeCodeOutputEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	counts := map[string]any{}
	for _, event := range events {
		assertMetricIdentityRetained(t, event)
		collectCodeOutputCount(t, event, counts)
	}
	assertCountsEqual(t, counts, map[string]int64{
		"lines_added_count":   128,
		"lines_removed_count": 12,
		"commit_count":        2,
		"pull_request_count":  1,
	})
	assertAttribution(t, events, "code-reviewer", map[string]any{"skill.name": "code-reviewer", "agent.name": "general-purpose"})
}

// collectCodeOutputCount records the canonical count(s) carried by one code-output
// event and enforces the model-promotion rule: lines_of_code promotes the observed
// model, commit/pull_request carry standard attrs only.
func collectCodeOutputCount(t *testing.T, event canonical.Event, counts map[string]any) {
	t.Helper()
	switch event.EventType {
	case linesOfCodeMetric:
		if event.Attributes["model"] != "claude-sonnet-5" {
			t.Fatalf("lines_of_code event must promote model, got %#v", event.Attributes["model"])
		}
		for _, key := range []string{"lines_added_count", "lines_removed_count"} {
			if value, ok := event.Attributes[key]; ok {
				counts[key] = value
			}
		}
	case commitMetric:
		assertNoModelPromotion(t, event)
		counts["commit_count"] = event.Attributes["commit_count"]
	case pullRequestMetric:
		assertNoModelPromotion(t, event)
		counts["pull_request_count"] = event.Attributes["pull_request_count"]
	default:
		t.Fatalf("unexpected event type %q", event.EventType)
	}
}

// assertNoModelPromotion proves a commit/pull_request event carries standard
// attrs only, never a promoted model.
func assertNoModelPromotion(t *testing.T, event canonical.Event) {
	t.Helper()
	if _, hasModel := event.Attributes["model"]; hasModel {
		t.Fatalf("%s must not promote model (standard attrs only): %#v", event.EventType, event.Attributes)
	}
}

// assertCountsEqual proves every expected canonical count landed with its exact
// value.
func assertCountsEqual(t *testing.T, counts map[string]any, want map[string]int64) {
	t.Helper()
	for key, value := range want {
		if counts[key] != value {
			t.Fatalf("%s = %#v, want %d", key, counts[key], value)
		}
	}
}

// TestNormalizeMetricsMalformedCodeOutputValueIsError proves the routing contract
// for the M11 counters: a lines_of_code/commit/pull_request datapoint with an
// unparseable count is a real error, NOT the skip sentinel, so the route does not
// silently 202-accept and drop supported Claude code-output data.
func TestNormalizeMetricsMalformedCodeOutputValueIsError(t *testing.T) {
	cases := map[string]string{
		"lines_of_code": `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.lines_of_code.count","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"added"}}],"asDouble":-4,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"commit":        `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.commit.count","sum":{"dataPoints":[{"attributes":[],"asDouble":-1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"pull_request":  `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.pull_request.count","sum":{"dataPoints":[{"attributes":[],"asDouble":1.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeMetrics([]byte(payload), time.Now().UTC())
			if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
				t.Fatalf("got %v, want a hard normalisation error for an unparseable %s count", err, name)
			}
		})
	}
}

// TestNormalizeMetricsUnrecognisedLinesOfCodeTypeIsSkipped proves a lines_of_code
// datapoint whose type is neither added nor removed is skipped (route-tolerated),
// not a hard error — a future category must not fail the batch.
func TestNormalizeMetricsUnrecognisedLinesOfCodeTypeIsSkipped(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.lines_of_code.count","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"moved"}}],"asDouble":9,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for an unrecognised lines_of_code type", err)
	}
}

func TestNormalizeMetricsEngagementGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-engagement-metrics.json")
	receivedAt := time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC)

	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeEngagementEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-engagement-metrics.events.json", first)
}

// assertClaudeEngagementEvents proves the M12 outcome: edit-decision and session
// counts land on a single canonical key each, their categoricals survive as
// dimensions in metric_attributes (so the edit-acceptance rate is a group-by
// decision, never dropping an unforeseen value), active-time lands as float
// seconds keyed by a promoted activity_type (with its raw wire `type` also
// retained raw in metric_attributes), and operator identity is retained raw and
// routed into environment (#173), never amputated at ingest.
func assertClaudeEngagementEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	editByDecision := map[string]int64{}
	sessionByStart := map[string]int64{}
	activeByType := map[string]float64{}
	for _, event := range events {
		attrs := event.ProviderExtensions["metric_attributes"].(map[string]any)
		assertMetricIdentityRetained(t, event)
		collectEngagementEvent(t, event, attrs, editByDecision, sessionByStart, activeByType)
	}
	if editByDecision["accept"] != 5 || editByDecision["reject"] != 1 {
		t.Fatalf("edit_decision_count by decision = %#v, want accept:5 reject:1", editByDecision)
	}
	if sessionByStart["fresh"] != 1 || sessionByStart["resume"] != 1 {
		t.Fatalf("session_count by start_type = %#v, want fresh:1 resume:1", sessionByStart)
	}
	if activeByType["user"] != 42.5 || activeByType["cli"] != 108 {
		t.Fatalf("active_time_seconds by type = %#v, want user:42.5 cli:108", activeByType)
	}
}

// collectEngagementEvent routes one engagement event into its by-category tally,
// proving the per-type invariants along the way: edit-decision and session.count
// keep their categoricals as surviving dims (never a model promotion), and
// active_time carries a promoted activity_type while its raw wire `type` is also
// retained raw in metric_attributes (#173 — nothing dropped at ingest).
func collectEngagementEvent(t *testing.T, event canonical.Event, attrs map[string]any, editByDecision, sessionByStart map[string]int64, activeByType map[string]float64) {
	t.Helper()
	assertNoModelPromotion(t, event)
	switch event.EventType {
	case editDecisionMetric:
		assertSurvivingDims(t, "edit-decision", attrs, "decision", "tool_name", "source", "language")
		editByDecision[attrs["decision"].(string)] = event.Attributes["edit_decision_count"].(int64)
	case sessionCountMetric:
		assertSurvivingDims(t, "session.count", attrs, "start_type")
		sessionByStart[attrs["start_type"].(string)] = event.Attributes["session_count"].(int64)
	case activeTimeMetric:
		if _, retained := attrs["type"]; !retained {
			t.Fatalf("active_time raw wire type must be retained raw in metric_attributes (#173): %#v", attrs)
		}
		activeByType[event.Attributes["activity_type"].(string)] = event.Attributes["active_time_seconds"].(float64)
	default:
		t.Fatalf("unexpected event type %q", event.EventType)
	}
}

// assertSurvivingDims fails unless every named dim key is present in attrs.
func assertSurvivingDims(t *testing.T, kind string, attrs map[string]any, dims ...string) {
	t.Helper()
	for _, dim := range dims {
		if _, ok := attrs[dim]; !ok {
			t.Fatalf("%s must keep %q as a surviving dim: %#v", kind, dim, attrs)
		}
	}
}

// TestNormalizeMetricsMalformedEngagementValueIsError proves the M12 routing
// contract: an edit-decision/session/active-time datapoint with a negative or
// unparseable value is a real error, NOT the skip sentinel, so the route does not
// silently 202-accept and drop supported Claude engagement data.
func TestNormalizeMetricsMalformedEngagementValueIsError(t *testing.T) {
	cases := map[string]string{
		"edit_decision": `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.code_edit_tool.decision","sum":{"dataPoints":[{"attributes":[{"key":"decision","value":{"stringValue":"accept"}}],"asDouble":-2,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"session":       `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.session.count","sum":{"dataPoints":[{"attributes":[{"key":"start_type","value":{"stringValue":"fresh"}}],"asDouble":1.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"active_time":   `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.active_time.total","unit":"s","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"user"}}],"asDouble":-0.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeMetrics([]byte(payload), time.Now().UTC())
			if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
				t.Fatalf("got %v, want a hard normalisation error for an unparseable %s value", err, name)
			}
		})
	}
}

// TestNormalizeMetricsActiveTimeFractionalSecondsAccepted proves active_time takes
// the float duration path: a fractional second is preserved, not rejected as the
// integer counters reject a non-integer value — a valid exporter double must not
// be dropped.
func TestNormalizeMetricsActiveTimeFractionalSecondsAccepted(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.active_time.total","unit":"s","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"cli"}}],"asDouble":12.75,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("fractional active_time must be accepted, got %v", err)
	}
	if len(events) != 1 || events[0].Attributes["active_time_seconds"] != 12.75 {
		t.Fatalf("active_time_seconds = %#v, want a single 12.75 event", events)
	}
}

// TestNormalizeMetricsMalformedCostValueIsError proves the routing contract for
// cost.usage: a Claude cost datapoint with an unparseable USD value is a real
// error, NOT the skip sentinel, so the route does not silently 202-accept and
// drop supported Claude cost data.
func TestNormalizeMetricsMalformedCostValueIsError(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.cost.usage","sum":{"dataPoints":[{"attributes":[{"key":"model","value":{"stringValue":"claude-sonnet-5"}}],"asDouble":-0.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want a hard normalisation error for an unparseable cost value", err)
	}
}

// TestNormalizeMetricsRejectsNonClaudeService proves a Codex metrics payload is
// left for the Codex adapter (sentinel, not misattributed) so mixed-tool batches
// are safe.
func TestNormalizeMetricsRejectsNonClaudeService(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}}],"sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

// TestNormalizeMetricsUnmappedClaudeMetricIsTolerated proves a Claude resource
// carrying only an unmapped metric is accepted without a hard error — it yields
// the skip sentinel so the route 202-accepts it. Every documented Claude Code
// metric is now mapped (token/cost #89/#97, code-output #98, engagement #99), so
// the tolerated example uses a deliberately hypothetical future metric name.
func TestNormalizeMetricsUnmappedClaudeMetricIsTolerated(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.future_unmapped.count","sum":{"dataPoints":[{"attributes":[{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}}],"asDouble":1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for an unmapped Claude metric", err)
	}
}

// TestNormalizeMetricsMalformedTokenValueIsError proves the #89 routing contract:
// a Claude token.usage datapoint with a recognised token type but an unparseable
// value is a real error, NOT the skip sentinel, so the route does not silently
// 202-accept and drop supported Claude data.
func TestNormalizeMetricsMalformedTokenValueIsError(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}}],"asDouble":-1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want a hard normalisation error for an unparseable token value", err)
	}
}

// TestNormalizeMetricsRetainsAllAttributesRaw proves #173's raw-capture outcome:
// every datapoint and resource attribute is retained raw under provider_extensions
// — the pre-#173 allow-list no longer amputates identity or any unforeseen
// attribute at the local-only ingest boundary (epic #87). Operator identity is
// additionally routed into provider_extensions.environment so actor_id derives
// from it. What (if anything) to hide is a downstream visibility layer, never an
// ingest-time drop, so even a credential-shaped attribute is retained raw here:
// dropping it would reintroduce exactly the ingest amputation #173 removes.
func TestNormalizeMetricsRetainsAllAttributesRaw(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}},{"key":"os.type","value":{"stringValue":"linux"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},{"key":"query_source","value":{"stringValue":"main"}},{"key":"session.id","value":{"stringValue":"synthetic-session"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-token"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"file.path","value":{"stringValue":"/home/tiq-canary/secret"}}],"asDouble":12,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	event := events[0]
	attrs := event.ProviderExtensions["metric_attributes"].(map[string]any)
	for key, want := range map[string]any{
		"query_source":  "main",
		"session.id":    "synthetic-session",
		"user.email":    "synthetic@example.test",
		"file.path":     "/home/tiq-canary/secret",
		"api_key":       "tiq-canary-api-key",
		"authorization": "Bearer tiq-canary-token",
	} {
		if attrs[key] != want {
			t.Fatalf("attribute %q = %#v, want %#v retained raw in metric_attributes", key, attrs[key], want)
		}
	}
	resource := event.ProviderExtensions["resource"].(map[string]any)
	if resource["os.type"] != "linux" {
		t.Fatalf("resource attribute os.type not retained raw: %#v", resource)
	}
	environment := event.ProviderExtensions["environment"].(map[string]any)
	if environment["user_email"] != "synthetic@example.test" {
		t.Fatalf("user.email must route into environment: %#v", environment)
	}
	if environment["os_type"] != "linux" {
		t.Fatalf("resource os.type must route into environment: %#v", environment)
	}
	if event.ActorID != nativeSessionPrefix+"synthetic@example.test" {
		t.Fatalf("actor_id = %q, want it derived from the retained identity", event.ActorID)
	}
}

// TestNormalizeMetricsRetainsLargeIntegerAttributeRaw proves the raw metric-
// attribute decoder preserves an int64 beyond a float64's 53-bit mantissa: an OTLP
// intValue (encoded as a string per OTLP/JSON) is retained as a json.Number so it
// round-trips exactly rather than being rounded by attributeValue's ParseFloat,
// which would be a silent raw-capture violation.
func TestNormalizeMetricsRetainsLargeIntegerAttributeRaw(t *testing.T) {
	const large = "9007199254740993" // 2^53 + 1, not representable exactly as a float64
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"request.sequence","value":{"intValue":"9007199254740993"}}],"asDouble":10,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	attrs := events[0].ProviderExtensions["metric_attributes"].(map[string]any)
	got, ok := attrs["request.sequence"].(json.Number)
	if !ok {
		t.Fatalf("large integer attribute must be retained as json.Number, got %T %#v", attrs["request.sequence"], attrs["request.sequence"])
	}
	if got.String() != large {
		t.Fatalf("large integer attribute = %q, want %q retained without precision loss", got.String(), large)
	}
	encoded, err := json.Marshal(events[0])
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if !strings.Contains(string(encoded), large) {
		t.Fatalf("large integer must round-trip exactly in marshalled JSON: %s", encoded)
	}
}

// FuzzNormalizeMetrics exercises the metrics ingest boundary (#173 added raw
// attribute retention + a precision-preserving decoder + environment routing to
// it): a malformed, truncated, or adversarially-shaped OTLP metrics payload must
// never panic — an unforeseen AnyValue shape, a non-numeric intValue, an empty
// value ({}), or a nested resource must be tolerated (skipped or retained raw),
// not crash the normaliser. Seeds cover the observed shape plus the raw-retention
// edge cases the allow-list previously never reached.
func FuzzNormalizeMetrics(f *testing.F) {
	f.Add([]byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}},{"key":"host.arch","value":{"stringValue":"amd64"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},{"key":"query_source","value":{"stringValue":"main"}},{"key":"session.id","value":{"stringValue":"s1"}},{"key":"user.id","value":{"stringValue":"u1"}}],"asDouble":10,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`))
	f.Add([]byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"request.sequence","value":{"intValue":"9007199254740993"}},{"key":"user.email","value":{"stringValue":"a@b.test"}}],"asDouble":10,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`))
	f.Add([]byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"workspace.host_paths","value":{"arrayValue":{"values":[{"stringValue":"/repo"}]}}},{"key":"weird","value":{"kvlistValue":{"values":[]}}},{"key":"empty","value":{}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}}],"asInt":"5","timeUnixNano":"x"}]}}]}]}]}`))
	f.Add([]byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{}]}}]}]}]}`))
	f.Add([]byte("not json"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		events, err := NormalizeMetrics(data, time.Unix(0, 0).UTC())
		if err != nil {
			return
		}
		// A produced event must marshal cleanly: the raw-retention path stores
		// json.Number and raw OTLP value maps in provider_extensions, so a decode
		// shape that cannot round-trip would be a raw-capture regression, not just
		// a panic.
		if _, err := json.Marshal(events); err != nil {
			t.Fatalf("normalised metrics must marshal: %v", err)
		}
	})
}

// TestNormalizeMetricsDistinctEventIDsForSameTimestamp proves two token
// datapoints sharing a session, type, and timestamp do not collide under
// CorrelateEvents (finding #8): the datapoint index keeps them distinct.
func TestNormalizeMetricsDistinctEventIDsForSameTimestamp(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"session.id","value":{"stringValue":"s1"}}],"asDouble":10,"timeUnixNano":"1789042160000000000"},{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"session.id","value":{"stringValue":"s1"}}],"asDouble":20,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 distinct datapoints", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must stay distinct for same-timestamp datapoints: %#v", events)
	}
}

// TestRawAttributeValueDecodesEachShape proves rawAttributeValue retains every
// OTLP AnyValue shape verbatim (#173 Site 5): scalars pass through, integers keep
// exact precision as json.Number, string arrays decode, and an unforeseen shape is
// retained as its raw value map rather than being dropped to nil.
func TestRawAttributeValueDecodesEachShape(t *testing.T) {
	cases := []struct {
		name   string
		value  map[string]any
		want   any
		wantOK bool
	}{
		{name: "empty is not retained", value: map[string]any{}, want: nil, wantOK: false},
		{name: "string", value: map[string]any{"stringValue": "raw"}, want: "raw", wantOK: true},
		{name: "bool", value: map[string]any{"boolValue": true}, want: true, wantOK: true},
		{name: "double", value: map[string]any{"doubleValue": 1.5}, want: 1.5, wantOK: true},
		{name: "int string keeps precision", value: map[string]any{"intValue": "9007199254740993"}, want: json.Number("9007199254740993"), wantOK: true},
		{name: "malformed int string remains raw", value: map[string]any{"intValue": "900719925474099�3"}, want: "900719925474099�3", wantOK: true},
		{name: "int float becomes number", value: map[string]any{"intValue": float64(42)}, want: json.Number("42"), wantOK: true},
		{name: "string array", value: map[string]any{"arrayValue": map[string]any{"values": []any{map[string]any{"stringValue": "a"}, map[string]any{"stringValue": "b"}}}}, want: []string{"a", "b"}, wantOK: true},
		{name: "unforeseen shape retained raw", value: map[string]any{"bytesValue": "deadbeef"}, want: map[string]any{"bytesValue": "deadbeef"}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rawAttributeValue(tc.value)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("value = %#v, want %#v", got, tc.want)
			}
		})
	}
}
