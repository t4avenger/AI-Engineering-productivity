// Package insights derives explainable findings from sanitised canonical data.
package insights

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const MCPSchemaVersion = "0.1.0"

type MCPInventory struct {
	SchemaVersion string      `json:"schema_version"`
	Totals        MCPTotals   `json:"totals"`
	Servers       []MCPServer `json:"servers"`
	Notes         []string    `json:"notes"`
}

type MCPTotals struct {
	ConnectedServers          int    `json:"connected_servers"`
	UsedServers               int    `json:"used_servers"`
	UnusedServers             int    `json:"unused_servers"`
	UsageUnavailableServers   int    `json:"usage_unavailable_servers"`
	RequestInputTokens        *int64 `json:"request_input_tokens"`
	RequestOutputTokens       *int64 `json:"request_output_tokens"`
	RequestCachedInputTokens  *int64 `json:"request_cached_input_tokens"`
	RequestCacheCreatedTokens *int64 `json:"request_cache_created_tokens"`
	TokenContextLabel         string `json:"token_context_label"`
}

type MCPServer struct {
	ServerFingerprint         string   `json:"server_fingerprint"`
	ServerName                string   `json:"server_name"`
	IdentityState             string   `json:"identity_state"`
	Provider                  string   `json:"provider"`
	Tool                      string   `json:"tool"`
	SessionID                 string   `json:"session_id"`
	ConnectionStatus          string   `json:"connection_status"`
	ConnectionScope           string   `json:"connection_scope"`
	TransportType             string   `json:"transport_type"`
	IsPlugin                  *bool    `json:"is_plugin"`
	InvocationCount           int      `json:"invocation_count"`
	ToolNames                 []string `json:"tool_names"`
	Used                      bool     `json:"used"`
	UsageState                string   `json:"usage_state"`
	ContextWasteState         string   `json:"context_waste_state"`
	RequestInputTokens        *int64   `json:"request_input_tokens"`
	RequestOutputTokens       *int64   `json:"request_output_tokens"`
	RequestCachedInputTokens  *int64   `json:"request_cached_input_tokens"`
	RequestCacheCreatedTokens *int64   `json:"request_cache_created_tokens"`
	TokenContextLabel         string   `json:"token_context_label"`
}

type tokenContext struct {
	input        *int64
	output       *int64
	cachedInput  *int64
	cacheCreated *int64
}

type mcpUsage struct {
	count       int
	serverNames map[string]struct{}
	toolNames   map[string]struct{}
}

type mcpUse struct {
	fingerprint string
	serverName  string
	toolName    string
}

// MCPInventoryFromEvents reports provider-reported MCP server names when
// available, privacy-safe fingerprints for correlation, observed connection
// metadata, and explicit MCP use. If no reviewed invocation signal is present,
// usage remains unavailable rather than inferred.
func MCPInventoryFromEvents(events []canonical.Event) MCPInventory {
	servers := map[string]*MCPServer{}
	used := map[string]mcpUsage{}
	tokensBySession := map[string]tokenContext{}

	for _, event := range events {
		if context, ok := requestTokenContext(event); ok {
			tokensBySession[event.SessionID] = mergeTokenContext(tokensBySession[event.SessionID], context)
		}
		if use, ok := mcpUseEvent(event); ok {
			used[use.fingerprint] = mergeMCPUsage(used[use.fingerprint], use)
			if _, exists := servers[use.fingerprint]; !exists {
				servers[use.fingerprint] = &MCPServer{
					ServerFingerprint: use.fingerprint,
					ServerName:        use.serverName,
					IdentityState:     usageIdentityState(use.serverName),
					Provider:          event.Provider,
					Tool:              event.Tool,
					SessionID:         event.SessionID,
					ConnectionStatus:  "unavailable",
					ConnectionScope:   "unknown",
					TransportType:     "unknown",
					ToolNames:         []string{},
					UsageState:        "unavailable",
					ContextWasteState: "usage_unavailable",
				}
			}
		}
		if server, ok := mcpConnection(event); ok {
			servers[server.ServerFingerprint] = &server
		}
	}

	result := MCPInventory{
		SchemaVersion: MCPSchemaVersion,
		Servers:       []MCPServer{},
		Notes: []string{
			"Request token context is session/request-level only and is not an exact per-MCP allocation.",
			"MCP usage is marked observed only when an explicit invocation signal carries the same privacy-safe fingerprint.",
		},
	}
	for _, server := range servers {
		result.Servers = append(result.Servers, annotatedServer(server, used, tokensBySession))
	}
	sort.Slice(result.Servers, func(i, j int) bool {
		left, right := result.Servers[i], result.Servers[j]
		return fmt.Sprintf("%s:%s:%s", left.Provider, left.Tool, left.ServerFingerprint) < fmt.Sprintf("%s:%s:%s", right.Provider, right.Tool, right.ServerFingerprint)
	})
	result.Totals = mcpTotals(result.Servers)
	return result
}

func annotatedServer(server *MCPServer, used map[string]mcpUsage, tokensBySession map[string]tokenContext) MCPServer {
	server.ToolNames = []string{}
	if usage, ok := used[server.ServerFingerprint]; ok {
		server.Used = true
		server.InvocationCount = usage.count
		if server.ServerName == "" {
			server.ServerName = firstSortedValue(usage.serverNames)
		}
		server.ToolNames = sortedSet(usage.toolNames)
		server.UsageState = "observed"
		server.ContextWasteState = "used"
	} else if server.IdentityState == "fingerprinted" {
		server.UsageState = "not_observed"
		server.ContextWasteState = "connected_but_unused"
	} else {
		server.UsageState = "unavailable"
		server.ContextWasteState = "usage_unavailable"
	}
	context := tokensBySession[server.SessionID]
	server.RequestInputTokens = context.input
	server.RequestOutputTokens = context.output
	server.RequestCachedInputTokens = context.cachedInput
	server.RequestCacheCreatedTokens = context.cacheCreated
	server.TokenContextLabel = tokenContextLabel()
	return *server
}

func mcpTotals(servers []MCPServer) MCPTotals {
	totals := MCPTotals{TokenContextLabel: tokenContextLabel()}
	countedSessions := map[string]struct{}{}
	for _, server := range servers {
		totals.ConnectedServers++
		if server.Used {
			totals.UsedServers++
		}
		if server.ContextWasteState == "connected_but_unused" {
			totals.UnusedServers++
		}
		if server.ContextWasteState == "usage_unavailable" {
			totals.UsageUnavailableServers++
		}
		if _, counted := countedSessions[server.SessionID]; counted {
			continue
		}
		countedSessions[server.SessionID] = struct{}{}
		totals.RequestInputTokens = addOptional(totals.RequestInputTokens, server.RequestInputTokens)
		totals.RequestOutputTokens = addOptional(totals.RequestOutputTokens, server.RequestOutputTokens)
		totals.RequestCachedInputTokens = addOptional(totals.RequestCachedInputTokens, server.RequestCachedInputTokens)
		totals.RequestCacheCreatedTokens = addOptional(totals.RequestCacheCreatedTokens, server.RequestCacheCreatedTokens)
	}
	return totals
}

func mcpConnection(event canonical.Event) (MCPServer, bool) {
	if event.EventType != "mcp_server_connection" {
		return MCPServer{}, false
	}
	rawEvent, _ := event.ProviderExtensions["event"].(map[string]any)
	fingerprint, identityState := serverIdentity(event, rawEvent)
	return MCPServer{
		ServerFingerprint: fingerprint,
		ServerName:        stringValue(rawEvent, "server_name", ""),
		IdentityState:     identityState,
		Provider:          event.Provider,
		Tool:              event.Tool,
		SessionID:         event.SessionID,
		ConnectionStatus:  stringValue(rawEvent, "status", "unknown"),
		ConnectionScope:   stringValue(rawEvent, "server_scope", "unknown"),
		TransportType:     stringValue(rawEvent, "transport_type", "unknown"),
		IsPlugin:          boolPointer(rawEvent["is_plugin"]),
		ToolNames:         []string{},
		UsageState:        "unavailable",
		ContextWasteState: "usage_unavailable",
	}, true
}

func mcpUseEvent(event canonical.Event) (mcpUse, bool) {
	if event.EventType != "mcp_call" && event.EventType != "mcp.call" && event.Attributes["category"] != string(canonical.OperationCategoryMCPCall) {
		return mcpUse{}, false
	}
	if fingerprint, ok := hashedValue(event.ProviderExtensions); ok {
		return mcpUse{fingerprint: fingerprint}, true
	}
	if rawCall, ok := event.ProviderExtensions["mcp_call"].(map[string]any); ok {
		fingerprint, ok := hashedValue(rawCall)
		if !ok {
			return mcpUse{}, false
		}
		return mcpUse{fingerprint: fingerprint, serverName: stringValue(rawCall, "server_name", ""), toolName: stringValue(rawCall, "tool_name", "")}, true
	}
	return mcpUse{}, false
}

func mergeMCPUsage(current mcpUsage, use mcpUse) mcpUsage {
	if current.serverNames == nil {
		current.serverNames = map[string]struct{}{}
	}
	if current.toolNames == nil {
		current.toolNames = map[string]struct{}{}
	}
	current.count++
	if strings.TrimSpace(use.serverName) != "" {
		current.serverNames[use.serverName] = struct{}{}
	}
	if strings.TrimSpace(use.toolName) != "" {
		current.toolNames[use.toolName] = struct{}{}
	}
	return current
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func firstSortedValue(values map[string]struct{}) string {
	sorted := sortedSet(values)
	if len(sorted) == 0 {
		return ""
	}
	return sorted[0]
}

func serverIdentity(event canonical.Event, rawEvent map[string]any) (string, string) {
	serverName := stringValue(rawEvent, "server_name", "")
	if fingerprint, ok := hashedValue(rawEvent); ok {
		return fingerprint, usageIdentityState(serverName)
	}
	sum := sha256.Sum256([]byte(event.EventID))
	return "connection:" + hex.EncodeToString(sum[:16]), identityStateForName(serverName)
}

func usageIdentityState(name string) string {
	if strings.TrimSpace(name) != "" {
		return "provider_reported"
	}
	return "fingerprinted"
}

func identityStateForName(name string) string {
	if strings.TrimSpace(name) != "" {
		return "provider_reported"
	}
	return "unavailable"
}

func hashedValue(values map[string]any) (string, bool) {
	for _, key := range []string{"server_fingerprint", "server_hash", "server_hmac", "mcp_server_fingerprint", "mcp_server_hash", "mcp_server_hmac"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func requestTokenContext(event canonical.Event) (tokenContext, bool) {
	context := tokenContext{
		input:       intValue(event.Attributes["input_token_count"]),
		output:      intValue(event.Attributes["output_token_count"]),
		cachedInput: intValue(event.Attributes["cached_input_tokens"]),
	}
	if rawEvent, ok := event.ProviderExtensions["event"].(map[string]any); ok {
		context.input = firstInt(context.input, intValue(rawEvent["input_tokens"]))
		context.output = firstInt(context.output, intValue(rawEvent["output_tokens"]))
		context.cachedInput = firstInt(context.cachedInput, intValue(rawEvent["cache_read_tokens"]))
		context.cacheCreated = intValue(rawEvent["cache_creation_tokens"])
	}
	return context, context.input != nil || context.output != nil || context.cachedInput != nil || context.cacheCreated != nil
}

func mergeTokenContext(current, next tokenContext) tokenContext {
	return tokenContext{
		input:        addOptional(current.input, next.input),
		output:       addOptional(current.output, next.output),
		cachedInput:  addOptional(current.cachedInput, next.cachedInput),
		cacheCreated: addOptional(current.cacheCreated, next.cacheCreated),
	}
}

func tokenContextLabel() string {
	return "request-level context only; not exact per-MCP allocation"
}

func addOptional(left, right *int64) *int64 {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	sum := *left + *right
	return &sum
}

func firstInt(primary, fallback *int64) *int64 {
	if primary != nil {
		return primary
	}
	return fallback
}

func intValue(value any) *int64 {
	switch value := value.(type) {
	case int:
		v := int64(value)
		return &v
	case int64:
		return &value
	case float64:
		if value == float64(int64(value)) {
			v := int64(value)
			return &v
		}
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		var parsed int64
		if _, err := fmt.Sscan(value, &parsed); err == nil {
			return &parsed
		}
	}
	return nil
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func boolPointer(value any) *bool {
	if b, ok := value.(bool); ok {
		return &b
	}
	return nil
}
