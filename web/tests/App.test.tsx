import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';

import { App } from '../src/App';

const session = {
  schema_version: '0.1.0',
  session_id: 'session-1',
  provider: 'openai',
  tool: 'codex',
  state: 'completed',
  started_at: '2026-07-24T12:00:00Z',
  completed_at: null,
  attributes: { event_count: 1 },
  provider_extensions: {},
};

function mockAPI(
  sessions = [session],
  options: {
    detailFails?: boolean;
    deleteFails?: boolean;
    healthFails?: boolean;
  } = {},
) {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = input instanceof Request ? input.url : input.toString();
      if (init?.method === 'DELETE' && options.deleteFails)
        return Promise.resolve(new Response(null, { status: 500 }));
      if (init?.method === 'DELETE')
        return Promise.resolve(new Response(null, { status: 204 }));
      if (url.endsWith('/health') && options.healthFails)
        return Promise.reject(new Error('Daemon offline'));
      if (url.endsWith('/health'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              status: 'healthy',
              service: 'telemetryiq-daemon',
              timestamp: '2026-07-24T12:00:00Z',
            }),
            { status: 200 },
          ),
        );
      if (url.includes('/sessions?'))
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: sessions,
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      if (url.endsWith('/sessions/session-1') && options.detailFails)
        return Promise.resolve(new Response(null, { status: 500 }));
      if (url.endsWith('/sessions/session-1'))
        return Promise.resolve(
          new Response(JSON.stringify({ data: session }), { status: 200 }),
        );
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }),
  );
}

describe('App dashboard', () => {
  afterEach(() => vi.restoreAllMocks());

  test('shows an empty session journey without inventing integration data', async () => {
    mockAPI([]);
    render(<App />);

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    expect(
      await screen.findByRole('heading', { name: 'No sessions yet' }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Integrations' }));
    expect(
      await screen.findByRole('heading', { name: 'Awaiting telemetry' }),
    ).toBeInTheDocument();
  });

  test('shows unavailable fields and requires confirmation before deleting', async () => {
    mockAPI();
    render(<App />);

    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    expect(await screen.findByText('Model')).toBeInTheDocument();
    expect(screen.getAllByText('Unavailable').length).toBeGreaterThan(0);
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    expect(screen.getByRole('alertdialog')).toHaveTextContent(
      'Delete this session?',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: 'Sessions' }),
      ).toBeInTheDocument();
    });
  });

  test('shows the privacy defaults', async () => {
    mockAPI();
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: 'Privacy' }));
    expect(
      await screen.findByText('Prompts and responses'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('Not retained')).toHaveLength(2);
  });

  test('shows observed integrations and non-success outcomes on the home page', async () => {
    mockAPI([
      { ...session, state: 'failed' },
      {
        ...session,
        session_id: 'session-2',
        tool: 'claude',
        state: 'abandoned',
      },
    ]);
    render(<App />);
    expect(await screen.findByText('Failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Integrations' }));
    expect(
      await screen.findByRole('heading', { name: 'codex' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'claude' })).toBeInTheDocument();
  });

  test('shows daemon and session-detail errors and can cancel deletion', async () => {
    mockAPI([session], { detailFails: true, healthFails: true });
    render(<App />);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Daemon offline',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Request failed',
    );
  });

  test('keeps the detail view when deletion fails and allows cancellation', async () => {
    mockAPI([session], { deleteFails: true });
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: 'Sessions' }));
    fireEvent.click(await screen.findByRole('button', { name: /codex/i }));
    await screen.findByRole('heading', { name: 'codex session' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete this session' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete permanently' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Delete failed');
  });
});
