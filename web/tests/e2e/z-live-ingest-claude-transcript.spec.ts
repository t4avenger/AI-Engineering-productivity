import { expect, test } from '@playwright/test';

import {
  claudeLivePRLinkURL,
  claudePRLinkOTLPTraces,
  claudeTranscriptNDJSON,
  fetchLiveSessions,
  ingestClaudeTranscript,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for Claude session JSONL transcript import (F4, #91).
 * Drives the real daemon from playwright.config.ts — no page.route().fulfill()
 * mocking. POSTs NDJSON to /v1/claude/transcript, then asserts the UI renders
 * the session + assistant_message model/token counts without content canaries.
 * A second gate (#183) proves a tool-span full_command PR URL promotes to an
 * observed pr_link and renders on /pull-requests.
 */
const liveModel = 'tiq-live-e2e-transcript-model';

resetDaemonBetweenTests();

test('renders a Claude transcript session ingested through the live daemon', async ({
  page,
}) => {
  await ingestClaudeTranscript(claudeTranscriptNDJSON(liveModel));

  const sessions = await fetchLiveSessions();
  const transcriptSession = sessions.find(
    (session) => session.attributes?.model === liveModel,
  );
  expect(transcriptSession?.tool).toBe('claude-code');
  expect(transcriptSession?.attributes?.entrypoint).toBe('cli');
  expect(transcriptSession?.attributes?.git_branch).toBe('main');
  expect(transcriptSession?.availability?.entrypoint).toBe('observed');
  expect(transcriptSession?.availability?.git_branch).toBe('observed');
  expect(transcriptSession?.availability?.pr_link).toBe('unavailable');

  await unlockDashboard(page);
  await page.getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(
    page.getByRole('cell', { name: 'claude-code' }).first(),
  ).toBeVisible();
  await page.locator('table tbody a').first().click();
  await expect(page.getByRole('heading', { name: /^Session / })).toBeVisible();
  await expect(page.getByText('claude-code').first()).toBeVisible();
  await expect(page.getByText(liveModel).first()).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Environment' })).toBeVisible();
  await expect(page.getByText('cli', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('main', { exact: true }).first()).toBeVisible();
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

// #183: a Claude tool span whose raw full_command carries a verbatim
// pull-request URL must promote pr_link to observed through the same
// provider-agnostic aggregation the Codex path uses, and render the URL on
// /pull-requests — closing the always-unavailable Claude cell #158 shipped.
test('promotes a Claude tool-span PR URL to an observed pr_link on the live daemon', async ({
  page,
}) => {
  await unlockDashboard(page);
  await page.goto('/pull-requests');
  await expect(page.getByRole('link', { name: /github\.com/ })).toHaveCount(0);

  await ingestOTLPTraces(claudePRLinkOTLPTraces());

  const sessions = await fetchLiveSessions();
  const prSession = sessions.find(
    (session) =>
      session.session_id === 'claude-code:tiq-live-e2e-session-pr-link',
  );
  expect(prSession?.tool).toBe('claude-code');
  expect(prSession?.attributes?.pr_link).toBe(claudeLivePRLinkURL);
  expect(prSession?.availability?.pr_link).toBe('observed');

  await page.goto('/pull-requests');
  await expect(
    page.getByRole('link', { name: claudeLivePRLinkURL }),
  ).toBeVisible();
});
