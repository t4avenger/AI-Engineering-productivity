import { afterEach, describe, expect, test, vi } from 'vitest';

import {
  deleteSession,
  fetchEventProvenance,
  fetchSession,
  fetchSessionEvents,
  fetchSessions,
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
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            data: [],
            pagination: { limit: 100, next_cursor: null },
          }),
          { status: 200 },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    await fetchSessionEvents(
      '../../health?redirect=https://example.invalid',
      'a&limit=1',
    );
    await fetchEventProvenance('../sessions');

    const timelineURL = new URL(String(fetchMock.mock.calls[0]?.[0]));
    expect(timelineURL.origin).toBe('http://127.0.0.1:8080');
    expect(timelineURL.pathname).toBe(
      '/api/v1/sessions/..%2F..%2Fhealth%3Fredirect%3Dhttps%3A%2F%2Fexample.invalid/events',
    );
    expect(timelineURL.searchParams.get('cursor')).toBe('a&limit=1');
    expect(timelineURL.searchParams.get('limit')).toBe('100');

    const provenanceURL = new URL(String(fetchMock.mock.calls[1]?.[0]));
    expect(provenanceURL.pathname).toBe(
      '/api/v1/events/..%2Fsessions/provenance',
    );
  });
});
