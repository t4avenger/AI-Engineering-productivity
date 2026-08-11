import { MantineProvider } from '@mantine/core';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { App } from '../src/App';

const session = {
  schema_version: '0.1.0',
  session_id: 'session-1',
  provider: 'openai',
  tool: 'codex',
  state: 'completed',
  started_at: '2026-07-24T12:00:00Z',
  completed_at: null,
  attributes: { event_count: 2 },
  provider_extensions: {},
};
const event = {
  event_id: 'event-1',
  event_type: 'codex.sse_event',
  occurred_at: '2026-07-24T12:00:00Z',
  received_at: '2026-07-24T12:00:01Z',
  provider: 'openai',
  tool: 'codex',
  source_version: '0.145.0',
  model: 'synthetic-model',
  input_token_count: '100',
  output_token_count: '10',
  unavailable_fields: ['cache_usage'],
};

afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
});

test('shows paginated timeline and safe provenance', async () => {
  sessionStorage.setItem('telemetryiq-auth-token', 'test-token');
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (url.endsWith('/health'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              status: 'healthy',
              service: 'daemon',
              timestamp: '2026-07-24T12:00:00Z',
            }),
            { status: 200 },
          ),
        );
      if (url.includes('/sessions?'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [session],
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      if (
        url.includes('/sessions/session-1/events?') &&
        url.includes('cursor=')
      )
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [
                { ...event, event_id: 'event-2', event_type: 'codex.done' },
              ],
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      if (url.includes('/sessions/session-1/events?'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [event],
              pagination: { limit: 100, next_cursor: 'next' },
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/events/event-1/provenance'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [
                {
                  path: 'attributes.model',
                  action: 'retained',
                  reason: 'operational metadata',
                },
              ],
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/sessions/session-1'))
        return Promise.resolve(
          new Response(JSON.stringify({ data: session }), { status: 200 }),
        );
      if (init?.method === 'DELETE')
        return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.reject(new Error('Unexpected request: ' + url));
    }),
  );
  render(
    <MantineProvider env="test">
      <App />
    </MantineProvider>,
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Sessions' }));
  fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
  expect(
    await screen.findByRole('heading', { name: 'Event timeline' }),
  ).toBeInTheDocument();
  expect(
    await screen.findByText('synthetic-model', { exact: false }),
  ).toBeInTheDocument();
  expect(screen.getByText('Unavailable: cache_usage')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'View provenance' }));
  expect(await screen.findByLabelText('Event provenance')).toHaveTextContent(
    'attributes.model',
  );
  fireEvent.click(screen.getByRole('button', { name: 'Load more events' }));
  expect(
    await screen.findByRole('heading', { name: 'codex.done' }),
  ).toBeInTheDocument();
});
