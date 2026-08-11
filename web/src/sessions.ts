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

export interface TimelineEvent {
  event_id: string;
  event_type: string;
  occurred_at: string;
  received_at: string;
  provider: string;
  tool: string;
  source_version: string;
  model: string | null;
  input_token_count: string | null;
  output_token_count: string | null;
  unavailable_fields: string[];
}

export interface Provenance {
  path: string;
  action: string;
  reason: string;
}

interface SessionListResponse {
  data: Session[];
  pagination: { limit: number; next_cursor: string | null };
}

const defaultAPIURL = 'http://127.0.0.1:8080/api/v1';
const env = import.meta.env as { readonly VITE_API_URL?: string };
const apiURL = env.VITE_API_URL ?? defaultAPIURL;

export class SessionAPIError extends Error {
  constructor(
    readonly status: number,
    operation: string,
  ) {
    super(operation + ' failed with status ' + String(status));
    this.name = 'SessionAPIError';
  }
}

function authHeaders(init?: RequestInit): Headers {
  const headers = new Headers(init?.headers);
  const token = sessionStorage.getItem('telemetryiq-auth-token');
  if (token) headers.set('Authorization', 'Bearer ' + token);
  return headers;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiURL + path, {
    ...init,
    headers: authHeaders(init),
  });
  if (!response.ok) {
    throw new SessionAPIError(response.status, 'Request');
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

export interface EventPage {
  data: TimelineEvent[];
  pagination: { limit: number; next_cursor: string | null };
}

function validCursor(cursor: string): string {
  if (cursor === '' || /[^A-Za-z0-9_-]/.test(cursor)) {
    throw new TypeError('Session event cursor was malformed');
  }
  return cursor;
}

export async function fetchSessionEvents(
  id: string,
  cursor?: string,
): Promise<EventPage> {
  const query = cursor
    ? '?limit=100&cursor=' + encodeURIComponent(validCursor(cursor))
    : '?limit=100';
  const response = await request<EventPage>(
    '/sessions/' + encodeURIComponent(id) + '/events' + query,
  );
  if (!Array.isArray(response.data)) {
    throw new TypeError('Session event response was malformed');
  }
  return response;
}

export async function fetchEventProvenance(id: string): Promise<Provenance[]> {
  const response = await request<{ data?: Provenance[] }>(
    '/events/' + encodeURIComponent(id) + '/provenance',
  );
  if (!Array.isArray(response.data)) {
    throw new TypeError('Event provenance response was malformed');
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
    headers: authHeaders(),
  });
  if (!response.ok) {
    throw new SessionAPIError(response.status, 'Delete');
  }
}

export async function deleteAllSessions(): Promise<void> {
  const response = await fetch(apiURL + '/sessions', {
    method: 'DELETE',
    headers: authHeaders(),
  });
  if (!response.ok) {
    throw new SessionAPIError(response.status, 'Delete');
  }
}
