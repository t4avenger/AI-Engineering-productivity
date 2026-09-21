package claude

import (
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// mcpToolNamePrefix is the literal Claude Code prepends to every MCP tool name
// (mcp__<server>__<tool>). It is defined once here so the operationCategory
// classifier and parseMCPToolName share one source of truth rather than each
// re-spelling the prefix.
const mcpToolNamePrefix = "mcp__"

// parseMCPToolName splits a Claude MCP tool name "mcp__<server>__<tool>" into its
// server and tool components. ok is false for any name lacking the prefix or the
// server/tool separator (or with an empty server or tool), so a non-MCP tool name
// is never misclassified and a malformed MCP name never yields an empty server the
// inventory matcher would reject anyway.
func parseMCPToolName(name string) (server, toolName string, ok bool) {
	if !strings.HasPrefix(name, mcpToolNamePrefix) {
		return "", "", false
	}
	rest := name[len(mcpToolNamePrefix):]
	separator := strings.Index(rest, "__")
	if separator <= 0 || separator+2 >= len(rest) {
		return "", "", false
	}
	return rest[:separator], rest[separator+2:], true
}

// mcpCallExtension builds the provider_extensions.mcp_call block the
// insights.mcpUseEvent matcher consumes to mark a connected MCP server as used.
// Both the OTLP tool_result path (stampMCPCorrelation) and the JSONL transcript
// path build their mcp_call block through this helper so the two ingest routes
// emit an identical correlation shape.
func mcpCallExtension(server, toolName string) map[string]any {
	return map[string]any{"server_name": server, "tool_name": toolName}
}

// stampMCPCorrelation marks a tool event as an MCP call so the MCP-inventory
// matcher (insights.mcpUseEvent) lights the server as used: it sets the shared
// category attribute and the mcp_call extension that matcher reads, and drops
// mcp_calls from the event's unavailable-field list because the event now carries
// that signal. This mirrors the Codex precedent
// (internal/normalize/codex/logs.go).
func stampMCPCorrelation(attributes, extensions map[string]any, server, toolName string) {
	attributes["category"] = string(canonical.OperationCategoryMCPCall)
	extensions["mcp_call"] = mcpCallExtension(server, toolName)
	if fields, ok := attributes["unavailable_fields"].([]string); ok {
		attributes["unavailable_fields"] = removeUnavailableField(fields, "mcp_calls")
	}
}

// removeUnavailableField returns fields without drop, allocating a fresh slice so
// the per-event-type template returned by unavailableFields is never mutated in
// place (it is shared across events of the same type).
func removeUnavailableField(fields []string, drop string) []string {
	pruned := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != drop {
			pruned = append(pruned, field)
		}
	}
	return pruned
}
