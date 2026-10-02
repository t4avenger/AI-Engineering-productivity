package codex

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ReconstructAgentRelations builds the Codex multi-agent tree exclusively from
// provider-native rollout identifiers observed in CLI 0.160.0. Child
// session_meta.source supplies the parent edge; task and activity records add
// exact trace, turn, lifecycle, token, operation, and evidence rollups. No
// timestamp, path, model, or content matching participates in lineage.
func ReconstructAgentRelations(events []canonical.Event) []canonical.AgentRelation {
	threads := collectAgentThreads(events)
	if len(threads) == 0 {
		return nil
	}
	relations := make([]canonical.AgentRelation, 0, len(threads))
	for _, thread := range threads {
		if thread.parentID == "" {
			continue
		}
		relations = append(relations, relationFromAgentThread(thread, threads))
	}
	sort.Slice(relations, func(i, j int) bool {
		if relations[i].SessionID != relations[j].SessionID {
			return relations[i].SessionID < relations[j].SessionID
		}
		return relations[i].AgentID < relations[j].AgentID
	})
	return relations
}

type codexAgentThread struct {
	id, parentID, traceID, turnID    string
	path, nickname, role             string
	retained                         bool
	depth                            *int64
	outcome                          *string
	input, output, cached, reasoning *int64
	duration, toolDuration           *int64
	operationCount                   *int64
	evidence                         []map[string]any
	spawn                            map[string]any
	parentCandidates                 []string
	seenOperations                   map[string]struct{}
}

func collectAgentThreads(events []canonical.Event) map[string]*codexAgentThread {
	threads := map[string]*codexAgentThread{}
	for _, event := range events {
		record := codexRolloutRecord(event)
		payload := codexAgentMap(record["payload"])
		if payload == nil {
			continue
		}
		threadID := strings.TrimPrefix(event.SessionID, codexSessionPrefix)
		thread := ensureAgentThread(threads, threadID)
		thread.retained = true
		thread.addEvidence(event)
		switch record["type"] {
		case "session_meta":
			thread.observeMetadata(payload)
		case "event_msg":
			thread.observeEvent(payload, event, threads)
		}
	}
	return threads
}

func ensureAgentThread(threads map[string]*codexAgentThread, id string) *codexAgentThread {
	thread := threads[id]
	if thread == nil {
		thread = &codexAgentThread{id: id, seenOperations: map[string]struct{}{}}
		threads[id] = thread
	}
	return thread
}

func (thread *codexAgentThread) observeMetadata(payload map[string]any) {
	if id := codexAgentString(payload["id"]); id != "" {
		thread.id = id
	}
	spawn := codexAgentMap(codexAgentMap(codexAgentMap(payload["source"])["subagent"])["thread_spawn"])
	if spawn == nil {
		return
	}
	thread.parentID = codexAgentString(spawn["parent_thread_id"])
	thread.path = codexAgentString(spawn["agent_path"])
	thread.nickname = codexAgentString(spawn["agent_nickname"])
	thread.role = codexAgentString(spawn["agent_role"])
	thread.depth = codexAgentInt(spawn["depth"])
}

func (thread *codexAgentThread) observeEvent(payload map[string]any, event canonical.Event, threads map[string]*codexAgentThread) {
	switch codexAgentString(payload["type"]) {
	case "task_started":
		thread.traceID = codexAgentString(payload["trace_id"])
		thread.turnID = codexAgentString(payload["turn_id"])
	case "task_complete":
		thread.turnID = firstAgentString(thread.turnID, codexAgentString(payload["turn_id"]))
		thread.duration = codexAgentInt(payload["duration_ms"])
		thread.setOutcome("completed")
	case "turn_aborted":
		outcome := codexAgentString(payload["reason"])
		if outcome == "" {
			outcome = "cancelled"
		}
		thread.setOutcome(outcome)
	case "token_count":
		thread.observeTokens(payload)
	case "item_completed":
		thread.observeItem(payload, event, threads)
	}
}

func (thread *codexAgentThread) observeTokens(payload map[string]any) {
	usage := codexAgentMap(codexAgentMap(payload["info"])["total_token_usage"])
	if usage == nil {
		return
	}
	thread.input = codexAgentInt(usage["input_tokens"])
	thread.output = codexAgentInt(usage["output_tokens"])
	thread.cached = codexAgentInt(usage["cached_input_tokens"])
	thread.reasoning = codexAgentInt(usage["reasoning_output_tokens"])
}

func (thread *codexAgentThread) observeItem(payload map[string]any, event canonical.Event, threads map[string]*codexAgentThread) {
	item := codexAgentMap(payload["item"])
	switch codexAgentString(item["type"]) {
	case "SubAgentActivity":
		thread.observeActivity(item, event, threads)
	case "CommandExecution", "FileChange", "McpToolCall":
		thread.observeOperation(item)
	}
}

func (thread *codexAgentThread) observeActivity(item map[string]any, event canonical.Event, threads map[string]*codexAgentThread) {
	childID := codexAgentString(item["agent_thread_id"])
	if childID == "" {
		return
	}
	child := ensureAgentThread(threads, childID)
	child.parentCandidates = appendAgentDistinct(child.parentCandidates, thread.id)
	if child.path == "" {
		child.path = codexAgentString(item["agent_path"])
	}
	kind := codexAgentString(item["kind"])
	if kind == "completed" || kind == "interrupted" || kind == "failed" || kind == "cancelled" {
		child.setOutcome(kind)
	}
	if kind != "started" || child.spawn != nil {
		return
	}
	child.spawn = map[string]any{
		"event_id": event.EventID,
		"call_id":  codexAgentString(item["id"]),
		"kind":     kind,
		"agent_id": thread.id,
	}
}

func (thread *codexAgentThread) observeOperation(item map[string]any) {
	id := codexAgentString(item["id"])
	if id == "" {
		return
	}
	if _, seen := thread.seenOperations[id]; seen {
		return
	}
	thread.seenOperations[id] = struct{}{}
	if thread.operationCount == nil {
		zero := int64(0)
		thread.operationCount = &zero
	}
	*thread.operationCount = *thread.operationCount + 1
	if duration := rolloutDurationMs(item["duration"]); duration != nil {
		thread.toolDuration = addAgentOptional(thread.toolDuration, duration)
	}
}

func (thread *codexAgentThread) addEvidence(event canonical.Event) {
	for _, evidence := range thread.evidence {
		if evidence["event_id"] == event.EventID {
			return
		}
	}
	thread.evidence = append(thread.evidence, map[string]any{
		"event_id":   event.EventID,
		"session_id": event.SessionID,
		"event_type": event.EventType,
	})
}

func (thread *codexAgentThread) setOutcome(value string) {
	if value == "" {
		return
	}
	if thread.outcome == nil || codexAgentOutcomeRank(value) > codexAgentOutcomeRank(*thread.outcome) {
		thread.outcome = &value
	}
}

func relationFromAgentThread(thread *codexAgentThread, threads map[string]*codexAgentThread) canonical.AgentRelation {
	sort.Slice(thread.evidence, func(i, j int) bool {
		left := codexAgentString(thread.evidence[i]["session_id"]) + "\x00" + codexAgentString(thread.evidence[i]["event_id"])
		right := codexAgentString(thread.evidence[j]["session_id"]) + "\x00" + codexAgentString(thread.evidence[j]["event_id"])
		return left < right
	})
	rootID, complete := codexAgentRoot(thread, threads)
	traceID := rootID
	traceSource := "root_thread_id"
	if root := threads[rootID]; root != nil && root.traceID != "" {
		traceID, traceSource = root.traceID, "root_task_started.trace_id"
	}
	relation := canonical.AgentRelation{
		SchemaVersion:       canonical.RecordSchemaVersion,
		RelationID:          traceID + ":" + thread.id,
		SessionID:           normalize.ProviderNativeSessionID(codexSessionPrefix, rootID),
		TraceID:             traceID,
		Provider:            "openai",
		Tool:                "codex",
		AgentID:             thread.id,
		ParentKind:          canonical.ParentKindMainSession,
		OperationCount:      thread.operationCount,
		Outcome:             thread.outcome,
		InputTokens:         thread.input,
		OutputTokens:        thread.output,
		CacheReadTokens:     thread.cached,
		ReasoningTokens:     thread.reasoning,
		ToolDurationMsTotal: thread.toolDuration,
		WallClockMs:         thread.duration,
		Provenance:          canonical.ProvenanceObserved,
	}
	if thread.parentID != rootID || !complete {
		parent := thread.parentID
		relation.ParentAgentID = &parent
		relation.ParentKind = canonical.ParentKindSubAgent
	}
	extensions := map[string]any{
		"codex": map[string]any{
			"thread_id":        thread.id,
			"parent_thread_id": thread.parentID,
			"root_thread_id":   rootID,
			"trace_id_source":  traceSource,
		},
		"evidence_events": thread.evidence,
	}
	codexExtension := extensions["codex"].(map[string]any)
	putCodexAgentValue(codexExtension, "turn_id", thread.turnID)
	putCodexAgentValue(codexExtension, "thread_trace_id", thread.traceID)
	putCodexAgentValue(codexExtension, "agent_path", thread.path)
	putCodexAgentValue(codexExtension, "agent_nickname", thread.nickname)
	putCodexAgentValue(codexExtension, "agent_role", thread.role)
	if thread.depth != nil {
		codexExtension["depth"] = *thread.depth
	}
	if thread.spawn != nil {
		spawn := make(map[string]any, len(thread.spawn))
		for key, value := range thread.spawn {
			spawn[key] = value
		}
		if thread.parentID == rootID && complete {
			delete(spawn, "agent_id")
		}
		extensions["spawn"] = spawn
	}
	parents := appendAgentDistinct(append([]string(nil), thread.parentCandidates...), thread.parentID)
	sort.Strings(parents)
	if len(parents) > 1 {
		extensions["parent_agent_ids"] = parents
	}
	relation.ProviderExtensions = extensions
	return relation
}

func codexAgentRoot(thread *codexAgentThread, threads map[string]*codexAgentThread) (string, bool) {
	seen := map[string]struct{}{thread.id: {}}
	current := thread
	for current.parentID != "" {
		parent := threads[current.parentID]
		if parent == nil || !parent.retained {
			return current.id, false
		}
		if _, loop := seen[parent.id]; loop {
			return thread.id, false
		}
		seen[parent.id] = struct{}{}
		current = parent
	}
	return current.id, true
}

func codexRolloutRecord(event canonical.Event) map[string]any {
	rollout := codexAgentMap(event.ProviderExtensions["rollout"])
	return codexAgentMap(rollout["record"])
}

func codexAgentMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func codexAgentString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstAgentString(current, candidate string) string {
	if current != "" {
		return current
	}
	return candidate
}

func putCodexAgentValue(values map[string]any, key, value string) {
	if value != "" {
		values[key] = value
	}
}

func appendAgentDistinct(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func codexAgentInt(value any) *int64 {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Int64()
		if err != nil || parsed < 0 {
			return nil
		}
		return &parsed
	}
	return normalize.OptionalTokenCount(value)
}

func addAgentOptional(total, value *int64) *int64 {
	if value == nil {
		return total
	}
	if total == nil {
		initial := *value
		return &initial
	}
	*total += *value
	return total
}

func codexAgentOutcomeRank(value string) int {
	switch value {
	case "interrupted", "cancelled", "failed":
		return 2
	case "completed":
		return 1
	default:
		return 0
	}
}
