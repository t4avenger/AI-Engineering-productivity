export type SessionState =
  | 'created'
  | 'active'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'abandoned'
  | 'unknown';

export interface Session {
  schema_version: string;
  session_id: string;
  provider: string;
  tool: string;
  state: SessionState;
  started_at: string;
  completed_at: string | null;
  attributes: Record<string, unknown>;
  provider_extensions: Record<string, unknown>;
}

interface SessionListResponse {
  data: Session[];
  pagination: { limit: number; next_cursor: string | null };
}

const defaultAPIURL = 'http://127.0.0.1:8080/api/v1';
const env = import.meta.env as { readonly VITE_API_URL?: string };
const apiURL = env.VITE_API_URL ?? defaultAPIURL;

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiURL}${path}`, init);
  if (!response.ok) {
    throw new Error(`Request failed with status ${String(response.status)}`);
  }
  return (await response.json()) as T;
}

export async function fetchSessions(): Promise<Session[]> {
  const response = await request<SessionListResponse>('/sessions?limit=100');
  if (!Array.isArray(response.data)) {
    throw new TypeError('Session list response was malformed');
  }
  return response.data;
}

export async function fetchSession(id: string): Promise<Session> {
  const response = await request<{ data?: Partial<Session> }>(
    `/sessions/${encodeURIComponent(id)}`,
  );
  if (
    response.data === undefined ||
    typeof response.data.session_id !== 'string'
  ) {
    throw new TypeError('Session detail response was malformed');
  }
  return response.data as Session;
}

export async function deleteSession(id: string): Promise<void> {
  const response = await fetch(`${apiURL}/sessions/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(`Delete failed with status ${String(response.status)}`);
  }
}
