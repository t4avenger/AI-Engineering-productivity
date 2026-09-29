import { expect, test } from '@playwright/test';

import {
  codexRolloutNDJSON,
  codexRolloutOTLPLogs,
  codexRolloutSessionID,
  expectSessionDetailHeading,
  fetchLiveSessions,
  ingestCodexRollout,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders an authenticated Codex rollout merged with exact-ID OTLP evidence', async ({
  page,
}) => {
  await ingestOTLPLogs(codexRolloutOTLPLogs());
  const rollout = codexRolloutNDJSON();
  await ingestCodexRollout(rollout);
  await ingestCodexRollout(rollout);

  const sessions = await fetchLiveSessions();
  const matching = sessions.filter(
    (session) => session.session_id === `codex:${codexRolloutSessionID}`,
  );
  expect(matching).toHaveLength(1);
  expect(matching[0]?.tool).toBe('codex');

  await unlockDashboard(page);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await page.locator('table tbody a').first().click();
  await expectSessionDetailHeading(page);
  // The compact lane is label/time-only; its accessible chronological detail
  // is the product surface that displays retained raw conversation text.
  const conversation = page.locator('#conversation');
  await expect(conversation).toContainText('tiq-live retained Codex prompt');
  await expect(conversation).toContainText('tiq-live retained Codex response');
});
