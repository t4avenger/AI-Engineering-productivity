package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

// agentTreeJSON is the raw decoded response so the contract asserts wire field
// names and nulls, not just what a typed struct would tolerate.
type agentTreeJSON struct {
	Data []map[string]any `json:"data"`
}

func getAgentTree(t *testing.T, serverURL, sessionID string, wantStatus int) agentTreeJSON {
	t.Helper()
	response, err := http.Get(serverURL + "/api/v1/sessions/" + url.PathEscape(sessionID) + "/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != wantStatus {
		t.Fatalf("agents status = %d, want %d", response.StatusCode, wantStatus)
	}
	var tree agentTreeJSON
	if wantStatus == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&tree); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

func ingestTracesFixture(t *testing.T, serverURL, name string) {
	t.Helper()
	response := postOTLPToPath(t, serverURL, "/v1/traces", metricsFixturePayloadBytes(t, name), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)
}

// TestSessionAgentsAPIContract drives the live 2.1.287 nested-spawn capture
// through /v1/traces and reads the stored tree back: the delegator is a
// main-session root, Explore nests under it, raw ids and nullable rollups are
// passed through, and evidence resolves to retained span events.
func TestSessionAgentsAPIContract(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	ingestTracesFixture(t, server.URL, "claude-code-2.1.287-subagent-spans-otlp.json")
	ingestTracesFixture(t, server.URL, "claude-code-2.1.268-trace-spans-otlp.json")

	tree := getAgentTree(t, server.URL, "claude-code:00000000-0000-4000-8000-000000000287", http.StatusOK)
	if len(tree.Data) != 1 {
		t.Fatalf("roots = %d, want 1: %#v", len(tree.Data), tree.Data)
	}
	assertLiveNestedSpawnTree(t, tree.Data[0])

	if empty := getAgentTree(t, server.URL, "claude-code:00000000-0000-4000-8000-000000000001", http.StatusOK); empty.Data == nil || len(empty.Data) != 0 {
		t.Fatalf("session without relations = %#v, want empty array", empty.Data)
	}
	getAgentTree(t, server.URL, "claude-code:missing", http.StatusNotFound)
}

// assertLiveNestedSpawnTree checks the 2.1.287 fixture's tree on the wire: the
// delegator root, the nested Explore child, explicit nulls, and resolved evidence.
func assertLiveNestedSpawnTree(t *testing.T, root map[string]any) {
	t.Helper()
	if root["agent_id"] != "a0000000000287001" || root["parent_state"] != "main_session_observed" ||
		root["subagent_type"] != "tiq-delegator" || root["parent_agent_id"] != nil {
		t.Fatalf("root = %#v", root)
	}
	if value, present := root["workflow_name"]; !present || value != nil {
		t.Fatalf("workflow_name = %#v present=%v, want explicit null (never observed)", value, present)
	}
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("children = %#v, want 1", children)
	}
	child := children[0].(map[string]any)
	if child["parent_state"] != "parent_agent_observed" || child["parent_agent_id"] != "a0000000000287001" || child["subagent_type"] != "Explore" {
		t.Fatalf("child = %#v", child)
	}
	evidence := child["evidence"].([]any)
	if len(evidence) != 7 || evidence[0].(map[string]any)["event_id"] == nil {
		t.Fatalf("child evidence = %#v, want 7 spans resolved to retained events", evidence)
	}
}

type failingAgentRelationReader struct{}

func (failingAgentRelationReader) ListAgentRelations(context.Context, storage.AgentRelationFilter) ([]canonical.AgentRelation, error) {
	return nil, errors.New("synthetic relation failure")
}

func TestSessionAgentsAPIReportsUnavailableAndFailedReaders(t *testing.T) {
	found := conversationSessionStub{found: true}
	cases := []struct {
		name string
		api  sessionAPI
		want int
	}{
		{"no relation reader", sessionAPI{sessions: found, eventReader: conversationEventReaderStub{}}, http.StatusServiceUnavailable},
		{"relation query fails", sessionAPI{sessions: found, eventReader: conversationEventReaderStub{}, agentRelations: failingAgentRelationReader{}}, http.StatusInternalServerError},
		{"event query fails", sessionAPI{sessions: found, eventReader: conversationEventReaderStub{err: errors.New("boom")}, agentRelations: emptyAgentRelationReader{}}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s/agents", nil)
			request.SetPathValue("id", "s")
			response := httptest.NewRecorder()
			tc.api.agents(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

type emptyAgentRelationReader struct{}

func (emptyAgentRelationReader) ListAgentRelations(context.Context, storage.AgentRelationFilter) ([]canonical.AgentRelation, error) {
	return []canonical.AgentRelation{}, nil
}
