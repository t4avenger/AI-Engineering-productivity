import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeContentOTLPLogs,
  claudeToolSpanFilePathOTLPTraces,
  expectSessionDetailHeading,
  expectSessionTraceLanes,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('assembles five-lane Session Trace from live retained evidence', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeContentOTLPLogs());
  await ingestOTLPTraces(claudeToolSpanFilePathOTLPTraces());

  const conversationSession = 'claude-code:tiq-live-e2e-conversation-content';
  const filesSession = 'claude-code:tiq-live-e2e-session-files';

  await unlockDashboard(page, authToken);
  await page.goto(`/sessions/${encodeURIComponent(conversationSession)}`);
  await expectSessionDetailHeading(page);
  await expectSessionTraceLanes(page);
  await expect(page.getByLabel('Conversation lane')).toContainText(
    'tiq-live-e2e retained user',
  );
  await page.getByLabel('Conversation lane').getByRole('link').first().click();
  await expect(page.locator('#event-inspector')).toBeVisible();
  await expect(page.locator('#event-inspector')).toContainText('user_prompt');

  await page.goto(`/sessions/${encodeURIComponent(filesSession)}`);
  await expectSessionTraceLanes(page);
  await expect(page.getByLabel('Files lane')).toContainText(
    '/workspace/tiq-live-e2e-session-files.go',
  );
  await expect(page.getByLabel('Spans lane')).toBeVisible();
  await expect(page.getByText('tiq-canary-live-session-files')).toHaveCount(0);
});
