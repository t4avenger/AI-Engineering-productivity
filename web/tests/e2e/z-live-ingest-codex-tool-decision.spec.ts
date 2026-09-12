import { expect, test } from '@playwright/test';

import {
  authToken,
  codexToolDecisionOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders a Codex tool decision ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexToolDecisionOTLPLogs());

  const timeline = await fetch(
    'http://localhost:18080/api/v1/sessions/codex:tiq-live-e2e-decision-session/events?limit=10',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(timeline.status).toBe(200);
  const body = (await timeline.json()) as {
    data: Array<{
      event_type: string;
      approval_decision?: string;
      approval_reason_class?: string;
    }>;
  };
  expect(
    body.data.some(
      (event) =>
        event.event_type === 'codex.tool_decision' &&
        event.approval_decision === 'approved' &&
        event.approval_reason_class === 'policy',
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.goto('/sessions/codex:tiq-live-e2e-decision-session');
  await expect(page.getByText('Approval')).toBeVisible();
  await expect(page.getByText('approved')).toBeVisible();
  await expect(page.getByText('policy')).toBeVisible();
  await expect(page.getByText('functions/exec_command')).toBeVisible();
  await expect(page.getByText('tiq-canary-live-decision')).toHaveCount(0);
  await expect(page.getByText('decision-live@example.test')).toHaveCount(0);
});
