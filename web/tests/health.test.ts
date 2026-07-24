import { describe, expect, test, vi } from 'vitest';

import { fetchHealth } from '../src/health';

describe('fetchHealth', () => {
  test('rejects non-OK responses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('{}', { status: 503 }))),
    );

    await expect(fetchHealth('/health')).rejects.toThrow('503');
  });

  test('rejects malformed responses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify({ status: 'unknown' }))),
      ),
    );

    await expect(fetchHealth('/health')).rejects.toThrow('malformed');
  });
});
