import { afterEach, describe, expect, test, vi } from 'vitest';

import { deleteSession, fetchSession, fetchSessions } from '../src/sessions';

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
});
