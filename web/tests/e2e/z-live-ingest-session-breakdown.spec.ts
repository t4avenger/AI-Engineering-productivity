import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeToolSpanFilePathOTLPTraces,
  expectSessionBreakdownRail,
  fetchSessionBreakdown,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('projects evidence-based session duration breakdown into the right rail', async ({
  page,
}) => {
  await ingestOTLPTraces(claudeToolSpanFilePathOTLPTraces());

  const sessionId = 'claude-code:tiq-live-e2e-session-files';
  const breakdown = await fetchSessionBreakdown(sessionId);
  expect(breakdown.availability).toBe('available');
  expect(breakdown.calculation_version).toBe('1');
  expect(breakdown.window?.duration_ms).toBeGreaterThan(0);
  const toolCalls = breakdown.categories.find((category) => category.id === 'tool_calls');
  expect(toolCalls?.duration_ms).toBeGreaterThan(0);
  expect(toolCalls?.source_event_ids.length).toBeGreaterThan(0);

  await unlockDashboard(page, authToken);
  await page.goto(`/sessions/${encodeURIComponent(sessionId)}`);
  await expectSessionBreakdownRail(page, {
    available: true,
    categoryLabels: ['Tool calls'],
  });
  await expect(page.getByLabel('Session summary').getByText('Indeterminate').first()).toBeVisible();
});
