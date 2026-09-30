import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContentOTLPLogs,
  codexRawRetentionOTLPLogs,
  fetchEventDetail,
  fetchLiveSessionEvents,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('opens session event inspector from live retained conversation evidence', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeContentOTLPLogs());

  const sessionId = 'claude-code:tiq-live-e2e-conversation-content';
  const eventId = `${sessionId}:1`;

  const detail = await fetchEventDetail(sessionId, eventId);
  expect(detail.event_id).toBe(eventId);
  expect(detail.event_type).toBe('user_prompt');

  await unlockDashboard(page, authToken);
  await page.goto(
    `/sessions/${encodeURIComponent(sessionId)}?event=${encodeURIComponent(eventId)}&inspector=details`,
  );
  await expect(page.locator('#event-inspector')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'user_prompt' })).toBeVisible();
  await expect(page.locator(`.timeline-item[data-event-id="${eventId}"]`)).toHaveClass(/is-selected/);
  await expect(page.locator(`.conversation-item[data-event-id="${eventId}"]`)).toHaveClass(/is-selected/);

  await page.getByRole('tab', { name: 'Attributes' }).click();
  await expect(page.locator('#event-inspector')).toContainText('Provider extensions');
  await expect(page.locator('#event-inspector')).toContainText('tiq-live-e2e retained user');
  await expect(page).toHaveURL(new RegExp(`event=${encodeURIComponent(eventId)}`));
  await expect(page).toHaveURL(/inspector=attributes/);

  await page.goBack();
  await expect(page.locator('#event-inspector')).toBeVisible();
  await expect(page).toHaveURL(/inspector=details/);
});

test('shows retained Codex raw evidence from live ingest', async ({ page }) => {
  const sessionId = 'codex:tiq-live-e2e-codex-raw-retention';
  await ingestOTLPLogs(codexRawRetentionOTLPLogs());
  const events = await fetchLiveSessionEvents(sessionId);
  expect(events).toHaveLength(1);
  const eventId = events[0]?.event_id ?? '';
  const detail = await fetchEventDetail(sessionId, eventId);
  expect(JSON.stringify(detail.provider_extensions)).toEqual(
    expect.stringContaining('tiq-live-codex-source'),
  );

  await unlockDashboard(page, authToken);
  await page.goto(
    `/sessions/${encodeURIComponent(sessionId)}?event=${encodeURIComponent(eventId)}&inspector=attributes`,
  );
  const inspector = page.locator('#event-inspector');
  await expect(inspector).toBeVisible();
  for (const retained of [
    'tiq-live-codex-body',
    'tiq-live-codex-input',
    'tiq-live-codex-output',
    'tiq-live-codex-prompt',
    'tiq-live-codex-response',
    'tiq-live-codex-source',
    'tiq-live-codex@example.test',
  ]) {
    await expect(inspector).toContainText(retained);
  }
});
