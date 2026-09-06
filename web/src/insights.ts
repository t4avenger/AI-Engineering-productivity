import { apiRequestURL, request } from './sessions';

export interface MCPServerInsight {
  server_fingerprint: string;
  server_name: string;
  identity_state: string;
  provider: string;
  tool: string;
  session_id: string;
  connection_status: string;
  connection_scope: string;
  transport_type: string;
  is_plugin: boolean | null;
  invocation_count: number;
  tool_names: string[];
  used: boolean;
  usage_state: string;
  context_waste_state: string;
  request_input_tokens: number | null;
  request_output_tokens: number | null;
  request_cached_input_tokens: number | null;
  request_cache_created_tokens: number | null;
  token_context_label: string;
}

export interface MCPInventoryInsight {
  schema_version: string;
  totals: {
    connected_servers: number;
    used_servers: number;
    unused_servers: number;
    usage_unavailable_servers: number;
    request_input_tokens: number | null;
    request_output_tokens: number | null;
    request_cached_input_tokens: number | null;
    request_cache_created_tokens: number | null;
    token_context_label: string;
  };
  servers: MCPServerInsight[];
  notes: string[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object';
}

function isMCPInventory(value: unknown): value is MCPInventoryInsight {
  if (!isRecord(value) || !Array.isArray(value.servers)) return false;
  return isRecord(value.totals);
}

export async function fetchMCPInventory(): Promise<MCPInventoryInsight> {
  const response = await request<{ data?: unknown }>(
    apiRequestURL(['insights', 'mcp-inventory']),
  );
  if (!isMCPInventory(response.data)) {
    throw new TypeError('MCP inventory response was malformed');
  }
  return response.data;
}

export interface SkillRecordInsight {
  skill_name: string;
  provider: string;
  tool: string;
  detection_state: string;
  invocation_count: number;
  outcomes: Record<string, number>;
  outcome_state: string;
}

export interface SkillCoverageInsight {
  provider: string;
  tool: string;
  detection_state: string;
}

export interface SkillUsageInsight {
  schema_version: string;
  totals: {
    observed_skills: number;
    invocations: number;
    explicit_detection: number;
    inferred_detection: number;
    unavailable_detection: number;
    unknown_detection: number;
  };
  skills: SkillRecordInsight[];
  coverage: SkillCoverageInsight[];
  notes: string[];
}

function isSkillUsage(value: unknown): value is SkillUsageInsight {
  if (!isRecord(value)) return false;
  if (!Array.isArray(value.skills) || !Array.isArray(value.coverage)) {
    return false;
  }
  return isRecord(value.totals);
}

export async function fetchSkillUsage(): Promise<SkillUsageInsight> {
  const response = await request<{ data?: unknown }>(
    apiRequestURL(['insights', 'skill-usage']),
  );
  if (!isSkillUsage(response.data)) {
    throw new TypeError('Skill usage response was malformed');
  }
  return response.data;
}
