import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeCorrelationOTLPLogs,
  claudeLiveCorrelationPromptID,
  fetchEventDetail,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

// The #106 (X19) live-data gate: two user_prompt events sharing one prompt.id are
// POSTed to the real daemon's /v1/logs, then read back through the un-mocked read
// API and event inspector. Both events must expose the identical prompt.id under
// provider_extensions.correlation, so a downstream consumer can group a session's
// events by prompt — proven end to end, not just in the normaliser unit tests.
test('retains the shared prompt.id correlation key on every live-ingested event (#106)', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeCorrelationOTLPLogs());

  const sessionId = 'claude-code:tiq-live-e2e-correlation';
  for (const sequence of ['1', '2']) {
    const eventId = `${sessionId}:${sequence}`;
    const detail = await fetchEventDetail(sessionId, eventId);
    expect(detail.event_id).toBe(eventId);
    const correlation = detail.provider_extensions.correlation as
      | { prompt_id?: string }
      | undefined;
    expect(correlation?.prompt_id).toBe(claudeLiveCorrelationPromptID);
  }

  // The retained correlation id is also served through to the event inspector UI.
  const firstEventId = `${sessionId}:1`;
  await unlockDashboard(page, authToken);
  await page.goto(
    `/sessions/${encodeURIComponent(sessionId)}?event=${encodeURIComponent(firstEventId)}&inspector=attributes`,
  );
  await expect(page.locator('#event-inspector')).toBeVisible();
  await expect(page.locator('#event-inspector')).toContainText('Provider extensions');
  await expect(page.locator('#event-inspector')).toContainText(claudeLiveCorrelationPromptID);
});
