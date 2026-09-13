import { expect, test } from '@playwright/test';

import {
  authToken,
  codexCachedReasoningOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders Codex cached and reasoning tokens ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexCachedReasoningOTLPLogs());

  const timeline = await fetch(
    'http://localhost:18080/api/v1/sessions/codex:tiq-live-e2e-codex-token-session/events?limit=10',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(timeline.status).toBe(200);
  const body = (await timeline.json()) as {
    data: Array<{
      event_type: string;
      cached_input_token_count?: string;
      reasoning_token_count?: string;
      unavailable_fields?: string[];
    }>;
  };
  const event = body.data.find(
    (item) => item.event_type === 'codex.sse_event',
  );
  expect(event?.cached_input_token_count).toBe('300');
  expect(event?.reasoning_token_count).toBe('55');
  expect(event?.unavailable_fields ?? []).not.toContain('cache_usage');
  expect(event?.unavailable_fields ?? []).not.toContain('reasoning_tokens');

  await unlockDashboard(page, authToken);
  await page.goto('/sessions/codex:tiq-live-e2e-codex-token-session');
  await expect(page.getByText('Cached input tokens')).toBeVisible();
  await expect(page.getByText('Reasoning tokens')).toBeVisible();
  await expect(page.getByText('300')).toBeVisible();
  await expect(page.getByText('55 tokens')).toBeVisible();
  await expect(page.getByText('tiq-canary-live-codex-token')).toHaveCount(0);
});
