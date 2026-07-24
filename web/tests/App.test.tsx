import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';

import { App } from '../src/App';

describe('App health states', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test('renders loading state while health check is pending', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => undefined)),
    );

    render(<App />);

    expect(screen.getByRole('status')).toHaveTextContent('Checking daemon');
    expect(screen.getByText('Loading health state.')).toBeInTheDocument();
  });

  test('renders healthy state when daemon responds', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              status: 'healthy',
              service: 'telemetryiq-daemon',
              timestamp: '2026-07-24T12:00:00Z',
            }),
            { headers: { 'Content-Type': 'application/json' }, status: 200 },
          ),
        );
      }),
    );

    render(<App />);

    await waitFor(() => {
      expect(screen.getByText('Daemon healthy')).toBeInTheDocument();
    });
    expect(screen.getByText('telemetryiq-daemon')).toBeInTheDocument();
    expect(screen.getByText('2026-07-24T12:00:00Z')).toBeInTheDocument();
  });

  test('renders unhealthy state when daemon request fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => {
        return Promise.reject(new Error('Unable to reach daemon'));
      }),
    );

    render(<App />);

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Daemon unhealthy');
    });
    expect(screen.getByText('Unable to reach daemon')).toBeInTheDocument();
  });
});
