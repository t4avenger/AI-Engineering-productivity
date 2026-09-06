import { apiRequestURL, request } from './sessions';

export interface MCPServerInsight {
  server_fingerprint: string;
  identity_state: string;
  provider: string;
  tool: string;
  session_id: string;
  connection_status: string;
  connection_scope: string;
  transport_type: string;
  is_plugin: boolean | null;
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

export async function fetchMCPInventory(): Promise<MCPInventoryInsight> {
  const response = await request<{ data?: MCPInventoryInsight }>(
    apiRequestURL(['insights', 'mcp-inventory']),
  );
  if (
    response.data === undefined ||
    !Array.isArray(response.data.servers) ||
    typeof response.data.totals !== 'object'
  ) {
    throw new TypeError('MCP inventory response was malformed');
  }
  return response.data;
}
