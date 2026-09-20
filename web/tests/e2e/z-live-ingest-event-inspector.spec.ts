import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContentOTLPLogs,
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

  const api = await fetch(
    `http://localhost:18080/api/v1/sessions/${encodeURIComponent(sessionId)}/events/${encodeURIComponent(eventId)}`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(api.status).toBe(200);
  const payload = (await api.json()) as {
    data: { event_id: string; event_type: string; attributes: Record<string, unknown> };
  };
  expect(payload.data.event_id).toBe(eventId);
  expect(payload.data.event_type).toBe('user_prompt');

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
