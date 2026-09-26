import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeToolSpanFilePathOTLPTraces,
  codexThreadTurnOTLPLogs,
  codexThreadTurnOTLPTraces,
  expectSessionBreakdownRail,
  fetchSessionBreakdown,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

for (const scenario of [
  {
    name: 'projects Claude tool spans into the right rail',
    sessionId: 'claude-code:tiq-live-e2e-session-files',
    traces: claudeToolSpanFilePathOTLPTraces,
    category: 'tool_calls',
    label: 'Tool calls',
  },
  {
    name: 'projects exactly linked Codex turns as model generation',
    sessionId: 'codex:tiq-live-e2e-thread-218',
    logs: codexThreadTurnOTLPLogs,
    traces: codexThreadTurnOTLPTraces,
    category: 'model_generation',
    label: 'Model generation',
  },
]) {
  test(scenario.name, async ({ page }) => {
    if (scenario.logs) {
      await ingestOTLPLogs(scenario.logs());
    }
    await ingestOTLPTraces(scenario.traces());

    const breakdown = await fetchSessionBreakdown(scenario.sessionId);
    expect(breakdown.availability).toBe('available');
    expect(breakdown.calculation_version).toBe('1');
    expect(breakdown.window?.duration_ms).toBeGreaterThan(0);
    const category = breakdown.categories.find((item) => item.id === scenario.category);
    expect(category?.duration_ms).toBeGreaterThan(0);
    expect(category?.source_event_ids.length).toBeGreaterThan(0);

    await unlockDashboard(page, authToken);
    await page.goto(`/sessions/${encodeURIComponent(scenario.sessionId)}`);
    await expectSessionBreakdownRail(page, { available: true, categoryLabels: [scenario.label] });
    await expect(page.getByLabel('Session summary').getByText('Indeterminate').first()).toBeVisible();
  });
}
