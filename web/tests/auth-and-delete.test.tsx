import { MantineProvider } from '@mantine/core';
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
  attributes: {},
  provider_extensions: {},
};

function mockAPI(deleteFails = false) {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        input instanceof Request
          ? input.url
          : input instanceof URL
            ? input.href
            : input;
      if (url.endsWith('/health')) {
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
      }
      if (init?.method === 'DELETE' && deleteFails) {
        return Promise.resolve(new Response(null, { status: 500 }));
      }
      if (init?.method === 'DELETE') {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (url.includes('/sessions?')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              data: [session],
              pagination: { limit: 100, next_cursor: null },
            }),
            { status: 200 },
          ),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify({ data: session }), { status: 200 }),
      );
    }),
  );
}

describe('local API access and bulk deletion', () => {
  afterEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  test('stores a pasted token only for the browser session', async () => {
    mockAPI();
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    expect(
      screen.getByRole('heading', { name: 'Connect your dashboard' }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Local API token'), {
      target: { value: 'test-token' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Connect securely' }));
    await waitFor(() => {
      expect(sessionStorage.getItem('telemetryiq-auth-token')).toBe(
        'test-token',
      );
    });
    expect(fetch).toHaveBeenCalled();
  });

  test('requires an exact confirmation before deleting all retained telemetry', async () => {
    sessionStorage.setItem('telemetryiq-auth-token', 'test-token');
    mockAPI();
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Privacy' }));
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Delete all retained telemetry',
      }),
    );
    expect(
      await screen.findByRole('button', { name: 'Delete all permanently' }),
    ).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Confirmation'), {
      target: { value: 'DELETE ALL' },
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete all permanently' }),
    );
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  test('keeps the bulk-delete dialog open when deletion fails', async () => {
    sessionStorage.setItem('telemetryiq-auth-token', 'test-token');
    mockAPI(true);
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Privacy' }));
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Delete all retained telemetry',
      }),
    );
    fireEvent.change(screen.getByLabelText('Confirmation'), {
      target: { value: 'DELETE ALL' },
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete all permanently' }),
    );
    expect(await screen.findByRole('alert')).toHaveTextContent('Delete failed');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});
