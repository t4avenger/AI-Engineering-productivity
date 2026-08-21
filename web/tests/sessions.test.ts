import { afterEach, describe, expect, test, vi } from 'vitest';

import {
  deleteSession,
  fetchEventProvenance,
  fetchSession,
  fetchSessionEvents,
  fetchSessions,
  fetchCostSummary,
  fetchSessionCosts,
} from '../src/sessions';

describe('session API client', () => {
  afterEach(() => vi.restoreAllMocks());
  test('rejects malformed and unsuccessful responses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('{}', { status: 200 }))),
    );
    await expect(fetchSessions()).rejects.toThrow('malformed');
    await expect(fetchSession('a b')).rejects.toThrow('malformed');

    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(null, { status: 500 }))),
    );
    await expect(fetchSessions()).rejects.toThrow('500');
    await expect(deleteSession('a b')).rejects.toThrow('500');
  });

  test('keeps untrusted identifiers and cursors inside the API route', async () => {
    const requestedURLs: URL[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      requestedURLs.push(
        input instanceof URL
          ? input
          : new URL(input instanceof Request ? input.url : input),
      );
      return Promise.resolve(
        new Response(
          JSON.stringify({
            data: [],
            pagination: { limit: 100, next_cursor: null },
          }),
          { status: 200 },
        ),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    await fetchSessionEvents(
      '../../health?redirect=https://example.invalid',
      'a&limit=1',
    );
    await fetchEventProvenance('../sessions');

    const timelineURL = requestedURLs[0];
    expect(timelineURL.origin).toBe('http://127.0.0.1:8080');
    expect(timelineURL.pathname).toBe(
      '/api/v1/sessions/..%2F..%2Fhealth%3Fredirect%3Dhttps%3A%2F%2Fexample.invalid/events',
    );
    expect(timelineURL.searchParams.get('cursor')).toBe('a&limit=1');
    expect(timelineURL.searchParams.get('limit')).toBe('100');

    const provenanceURL = requestedURLs[1];
    expect(provenanceURL.pathname).toBe(
      '/api/v1/events/..%2Fsessions/provenance',
    );
  });

  test('reads cost summary and session records', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                currency: 'USD',
                calculated_amount_microusd: null,
                statuses: { unknown_price: 1 },
              },
            }),
            { status: 200 },
          ),
        ),
      ),
    );
    await expect(fetchCostSummary()).resolves.toMatchObject({
      statuses: { unknown_price: 1 },
    });
    await expect(fetchSessionCosts('session a')).rejects.toThrow('malformed');
  });
});
