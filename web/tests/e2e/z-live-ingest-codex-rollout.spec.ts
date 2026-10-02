import { expect, test } from '@playwright/test';

import {
  codexAgentBetaID,
  codexAgentCancelledID,
  codexAgentGammaID,
  codexAgentRootSessionID,
  codexMultiAgentRollouts,
  codexRolloutNDJSON,
  codexRolloutOTLPLogs,
  codexRolloutSessionID,
  expectSessionDetailHeading,
  expectSessionTraceLanes,
  fetchSessionAgents,
  fetchLiveSessions,
  ingestCodexRollout,
  ingestOTLPLogs,
  openLiveSessionTrace,
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
  await expectSessionTraceLanes(page);
  await expect(page.getByLabel('Tools & MCP lane')).toContainText('mcp call');
  await expect(page.getByLabel('Files lane')).toContainText('txt');
});

test('renders persisted Codex sibling, nested and interrupted agent relations', async ({
  page,
}) => {
  const rollouts = codexMultiAgentRollouts();
  for (const rollout of [...rollouts].reverse()) {
    await ingestCodexRollout(rollout);
  }
  await ingestCodexRollout(rollouts.at(-1)!);

  await expect
    .poll(() => fetchSessionAgents(codexAgentRootSessionID))
    .toEqual([
      expect.objectContaining({ parent_state: 'main_session_observed' }),
      expect.objectContaining({
        agent_id: codexAgentBetaID,
        parent_state: 'main_session_observed',
        children: [
          expect.objectContaining({
            agent_id: codexAgentGammaID,
            parent_state: 'parent_agent_observed',
            operation_count: 1,
            outcome: 'completed',
            reasoning_tokens: 4,
          }),
        ],
      }),
      expect.objectContaining({
        agent_id: codexAgentCancelledID,
        outcome: 'interrupted',
        operation_count: null,
      }),
    ]);

  await openLiveSessionTrace(page, codexAgentRootSessionID);
  const tree = page.getByRole('region', { name: 'Sub-agents' });
  const beta = tree.locator(`[data-agent-id="${codexAgentBetaID}"]`);
  const gamma = beta.locator(`[data-agent-id="${codexAgentGammaID}"]`);
  await expect(gamma).toContainText(`Spawned by ${codexAgentBetaID}`);
  await expect(gamma).toContainText('1 operations');
  await expect(gamma).toContainText('Reasoning tokens4');
  await expect(gamma).toContainText('Outcomecompleted');
  const cancelled = tree.locator(
    `[data-agent-id="${codexAgentCancelledID}"]`,
  );
  await expect(cancelled).toContainText('Outcomeinterrupted');
  await expect(cancelled).toContainText('not reported');

  await gamma
    .getByRole('link', { name: `Open event evidence for ${codexAgentGammaID}` })
    .click();
  await expect(page.locator('#event-inspector')).toBeVisible();
  await expect(page).toHaveURL(/codex-agent-gamma/);
});
