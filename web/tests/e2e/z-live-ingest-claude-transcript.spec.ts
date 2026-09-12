import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeTranscriptNDJSON,
  ingestClaudeTranscript,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Claude session JSONL transcript import (F4, #91).
 * Drives the real daemon from playwright.config.ts — no page.route().fulfill()
 * mocking. POSTs NDJSON to /v1/claude/transcript, then asserts the UI renders
 * the session + assistant_message model/token counts without content canaries.
 */
const liveModel = 'tiq-live-e2e-transcript-model';

resetDaemonBetweenTests();

test('renders a Claude transcript session ingested through the live daemon', async ({
  page,
}) => {
  await ingestClaudeTranscript(claudeTranscriptNDJSON(liveModel));

  const list = await fetch('http://localhost:18080/api/v1/sessions?limit=100', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(list.status).toBe(200);
  const listBody = (await list.json()) as {
    data: Array<{
      tool: string;
      session_id?: string;
      attributes?: { model?: string };
    }>;
  };
  expect(
    listBody.data.some((session) => session.tool === 'claude-code'),
  ).toBe(true);
  expect(
    listBody.data.some((session) => session.attributes?.model === liveModel),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('cell', { name: 'claude-code' }).first(),
  ).toBeVisible();
  await page.locator('table tbody a').first().click();
  await expect(page.getByRole('heading', { name: /^Session / })).toBeVisible();
  await expect(page.getByText('claude-code').first()).toBeVisible();
  await expect(page.getByText(liveModel).first()).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Assistant message' }),
  ).toBeVisible();
  await expect(page.getByText('assistant_message').first()).toBeVisible();
  await expect(page.getByText('2048 tokens').first()).toBeVisible();
  await expect(page.getByText('256 tokens').first()).toBeVisible();

  const pageText = await page.locator('body').innerText();
  for (const canary of [
    'tiq-canary-live-user-prompt',
    'tiq-canary-live-response',
    'tiq-canary-live-command',
    'tiq-canary-live-stdout',
    'tiq-canary-live-cwd',
  ]) {
    expect(pageText).not.toContain(canary);
  }
});
