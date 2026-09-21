import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContentOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders retained Claude conversation evidence ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeContentOTLPLogs());

  const sessionId = 'claude-code:tiq-live-e2e-conversation-content';
  const response = await fetch(
    `http://localhost:18080/api/v1/sessions/${encodeURIComponent(sessionId)}/conversation`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as {
    data: Array<{ role: string; text: string | null; content_availability: string }>;
  };
  expect(body.data).toEqual([
    expect.objectContaining({
      role: 'user',
      text: 'tiq-live-e2e retained user\nsecond line',
      content_availability: 'available',
    }),
    expect.objectContaining({
      role: 'assistant', text: '<REDACTED>', content_availability: 'provider_redacted' }),
    expect.objectContaining({
      role: 'unknown', text: 'tiq-live-e2e raw API evidence', content_availability: 'available',
    }),
  ]);

  await unlockDashboard(page, authToken);
  await page.goto(`/sessions/${encodeURIComponent(sessionId)}`);
  await expect(page.getByRole('heading', { name: 'Retained conversation evidence' })).toBeVisible();
  await expect(page.getByLabel('Conversation lane')).toBeVisible();
  const conversation = page.locator('#conversation');
  await expect(conversation.getByRole('heading', { name: 'User message' })).toBeVisible();
  await expect(
    conversation.getByText('tiq-live-e2e retained user\nsecond line'),
  ).toBeVisible();
  await expect(
    conversation.getByRole('heading', { name: 'Assistant response', exact: true }),
  ).toBeVisible();
  await expect(conversation.getByText('<REDACTED>', { exact: true })).toBeVisible();
  await expect(
    conversation.getByRole('heading', { name: 'API content evidence' }),
  ).toBeVisible();
  await expect(conversation.getByText('tiq-live-e2e raw API evidence')).toBeVisible();
});
