import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeSkillOTLPLogs,
  codexSkillOTLPMetrics,
  ingestOTLPLogs,
  ingestOTLPMetrics,
  resetDaemonBetweenTests,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for the skill usage insight: drive the real daemon
 * started by playwright.config.ts. No page.route().fulfill() mocking — ingest
 * real OTLP skill signals, then assert the Insights UI renders explicit skill
 * usage (QUALITY_GATES live-data DoD).
 */
resetDaemonBetweenTests();

test('renders explicit skill usage for data ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(claudeSkillOTLPLogs());
  await ingestOTLPMetrics(codexSkillOTLPMetrics());

  await page.goto('/');
  await page.getByLabel('Local API token').fill(authToken);
  const skillUsage = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/insights/skill-usage') &&
      response.request().method() === 'GET' &&
      response.status() === 200,
  );
  await page.getByRole('button', { name: 'Connect securely' }).click();
  await page.getByRole('button', { name: 'Insights' }).click();

  const body = (await (await skillUsage).json()) as {
    data: {
      skills: Array<{ skill_name: string }>;
      coverage: Array<{ tool: string; detection_state: string }>;
      totals: { observed_skills: number; explicit_detection: number };
    };
  };
  expect(body.data.totals.observed_skills).toBeGreaterThanOrEqual(2);
  expect(body.data.totals.explicit_detection).toBeGreaterThanOrEqual(2);
  expect(body.data.skills.some((row) => row.skill_name === 'tiq-probe')).toBe(
    true,
  );
  expect(
    body.data.coverage.some(
      (row) => row.tool === 'claude-code' && row.detection_state === 'explicit',
    ),
  ).toBe(true);
  expect(
    body.data.coverage.some(
      (row) => row.tool === 'codex' && row.detection_state === 'explicit',
    ),
  ).toBe(true);

  const skillSection = page.locator(
    'section[aria-labelledby="skill-usage-title"]',
  );
  await expect(
    skillSection.getByRole('heading', { name: 'Skill usage' }),
  ).toBeVisible();
  await expect(skillSection.getByText('tiq-probe').first()).toBeVisible();
  await expect(skillSection.getByText('claude-code').first()).toBeVisible();
  await expect(skillSection.getByText('codex').first()).toBeVisible();
  await expect(
    skillSection.getByText('explicit', { exact: true }).first(),
  ).toBeVisible();
});
