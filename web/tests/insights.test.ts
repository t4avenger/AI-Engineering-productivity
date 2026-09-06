import { afterEach, describe, expect, test, vi } from 'vitest';

import { fetchMCPInventory } from '../src/insights';

describe('insights API client', () => {
  afterEach(() => vi.restoreAllMocks());

  test('rejects null and primitive malformed MCP inventory responses', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify({ data: null }))),
      ),
    );
    await expect(fetchMCPInventory()).rejects.toThrow(
      'MCP inventory response was malformed',
    );

    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(JSON.stringify({ data: 1 })))),
    );
    await expect(fetchMCPInventory()).rejects.toThrow(
      'MCP inventory response was malformed',
    );
  });

  test('reads a valid MCP inventory response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(
            JSON.stringify({
              data: {
                schema_version: '0.1.0',
                totals: {
                  connected_servers: 0,
                  used_servers: 0,
                  unused_servers: 0,
                  usage_unavailable_servers: 0,
                  request_input_tokens: null,
                  request_output_tokens: null,
                  request_cached_input_tokens: null,
                  request_cache_created_tokens: null,
                  token_context_label:
                    'request-level context only; not exact per-MCP allocation',
                },
                servers: [],
                notes: [],
              },
            }),
          ),
        ),
      ),
    );

    await expect(fetchMCPInventory()).resolves.toMatchObject({
      schema_version: '0.1.0',
      servers: [],
    });
  });
});
