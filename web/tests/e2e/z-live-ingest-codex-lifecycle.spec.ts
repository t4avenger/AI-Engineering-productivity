import { expect, test } from '@playwright/test';

import {
  authToken,
  codexIntegrationStateOTLPMetrics,
  codexLifecycleOTLPLogs,
  codexLifecycleRolloutNDJSON,
  codexPRLinkOTLPLogs,
  ingestCodexRollout,
  ingestOTLPLogs,
  ingestOTLPMetrics,
  openSessionTraceDisclosures,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders Codex lifecycle signals ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexLifecycleOTLPLogs());
  await ingestCodexRollout(codexLifecycleRolloutNDJSON());
  await ingestOTLPMetrics(codexIntegrationStateOTLPMetrics());

  const sessions = await fetch('http://localhost:18080/api/v1/sessions?limit=20', {
    headers: { Authorization: `Bearer ${authToken}` },
  });
  expect(sessions.status).toBe(200);
  const sessionBody = (await sessions.json()) as {
    data: Array<{
      session_id: string;
      state: string;
      completed_at?: string | null;
      attributes: Record<string, string>;
      provider_extensions: Record<string, Record<string, string>>;
      availability: Record<string, string>;
    }>;
  };
  const session = sessionBody.data.find(
    (candidate) =>
      candidate.session_id === 'codex:tiq-live-e2e-lifecycle-session',
  );
  expect(session?.state).toBe('completed');
  expect(session?.completed_at).toBe('2026-09-06T18:02:47Z');
  expect(session?.attributes.entrypoint).toBe('codex exec');
  expect(session?.attributes.service_name).toBe('codex_exec');
  expect(session?.attributes.service_version).toBe('0.153.4');
  expect(session?.availability.entrypoint).toBe('observed');
  expect(session?.availability.tool_version).toBe('observed');
  expect(session?.availability.git_branch).toBe('unavailable');
  expect(session?.availability.pr_link).toBe('unavailable');
  expect(session?.provider_extensions.resource_attributes?.['service.name']).toBe(
    'codex_exec',
  );
  expect(session?.provider_extensions.correlation?.provider_session_id).toBe(
    'tiq-live-e2e-lifecycle-session',
  );

  const timeline = await fetch(
    'http://localhost:18080/api/v1/sessions/codex:tiq-live-e2e-lifecycle-session/events?limit=10',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(timeline.status).toBe(200);
  const timelineBody = (await timeline.json()) as {
    data: Array<{
      event_type: string;
      lifecycle_kind?: string;
      lifecycle_phase?: string;
      lifecycle_status?: string;
      entrypoint?: string;
      approval_policy?: string;
      sandbox_policy?: string;
      unavailable_fields?: string[];
    }>;
  };
  const start = timelineBody.data.find((event) => event.event_type === 'session.active');
  expect(start?.lifecycle_kind).toBe('session_start');
  expect(start?.entrypoint).toBe('codex exec');
  expect(start?.unavailable_fields ?? []).not.toContain('session_lifecycle');
  expect(
    timelineBody.data.some(
      (event) =>
        event.event_type === 'codex.startup_phase' &&
        event.lifecycle_phase === 'init' &&
        event.lifecycle_status === 'ok',
    ),
  ).toBe(true);

  const governanceStatesResponse = await fetch(
    'http://localhost:18080/api/v1/insights/governance-states',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(governanceStatesResponse.status).toBe(200);
  const governanceStatesBody = (await governanceStatesResponse.json()) as {
    data: { states: Array<{ kind: string; value: string; provenance: string }> };
  };
  expect(governanceStatesBody.data.states).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        kind: 'approval_policy',
        value: 'on-request',
        provenance: 'observed',
      }),
      expect.objectContaining({
        kind: 'approval_policy',
        value: 'never',
        provenance: 'observed',
      }),
      expect.objectContaining({
        kind: 'sandbox_policy',
        value: 'workspace-write',
        provenance: 'observed',
      }),
      expect.objectContaining({
        kind: 'sandbox_policy',
        value: 'read-only',
        provenance: 'observed',
      }),
    ]),
  );
  const governance = timelineBody.data.find(
    (event) =>
      event.event_type === 'codex.rollout.turn_context' &&
      event.sandbox_policy === 'workspace-write',
  );
  expect(governance?.approval_policy).toBe('never');
  expect(governance?.sandbox_policy).toBe('workspace-write');
  expect(
    timelineBody.data.some(
      (event) =>
        event.event_type === 'session.completed' &&
        event.lifecycle_status === 'completed',
    ),
  ).toBe(true);

  await unlockDashboard(page, authToken);
  await page.goto('/sessions/codex:tiq-live-e2e-lifecycle-session');
  await openSessionTraceDisclosures(page);
  await expect(page.getByRole('heading', { name: 'Environment' })).toBeVisible();
  await expect(page.getByText('codex_exec')).toBeVisible();
  await expect(page.getByText('0.153.4')).toBeVisible();
  await expect(page.getByText('codex exec').first()).toBeVisible();
  await expect(page.getByText('Branch').first()).toBeVisible();
  await expect(page.getByText('PR').first()).toBeVisible();
  const timelineUI = page.locator('#timeline');
  await expect(
    timelineUI
      .getByRole('link', { name: 'Session active', exact: true })
      .first(),
  ).toBeVisible();
  await expect(timelineUI.getByText('Lifecycle').first()).toBeVisible();
  await expect(timelineUI.getByText('session_start')).toBeVisible();
  await expect(timelineUI.getByText('task_complete')).toBeVisible();
  const governanceRow = timelineUI
    .locator('.timeline-item')
    .filter({ hasText: 'codex.rollout.turn_context' })
    .filter({ hasText: 'workspace-write' });
  await expect(governanceRow.getByText('Approval policy')).toBeVisible();
  await expect(governanceRow.getByText('workspace-write')).toBeVisible();
  await expect(page.getByText('tiq-canary-live-lifecycle')).toHaveCount(0);
  await expect(page.getByText('lifecycle-live@example.test')).toHaveCount(0);

  await page.goto('/governance');
  const providerGovernance = page.locator('#provider-governance-states');
  await expect(
    providerGovernance.getByRole('heading', {
      name: 'Observed provider governance state',
    }),
  ).toBeVisible();
  await expect(providerGovernance.getByText('on-request')).toBeVisible();
  await expect(providerGovernance.getByText('never')).toBeVisible();
  await expect(providerGovernance.getByText('workspace-write')).toBeVisible();
  await expect(providerGovernance.getByText('read-only')).toBeVisible();

  await page.goto('/integrations');
  const states = page.locator('#integration-states');
  await expect(
    states.getByRole('heading', { name: 'Observed integration states' }),
  ).toBeVisible();
  await expect(states.getByText('discovered')).toBeVisible();
  await expect(
    states.getByText('Discovery or cache activity does not prove use', {
      exact: false,
    }),
  ).toBeVisible();

  // #187 N02: live capture without pr_link stays an honest empty destination.
  await page.goto('/pull-requests');
  await expect(page.getByRole('heading', { name: 'Pull Requests' })).toBeVisible();
  await expect(
    page.getByText('No retained HTTP(S) pull-request URLs yet', { exact: false }),
  ).toBeVisible();
  await expect(page.getByRole('link', { name: /github\.com/ })).toHaveCount(0);

  await ingestOTLPLogs(codexPRLinkOTLPLogs());
  await page.goto('/pull-requests');
  await expect(
    page.getByRole('link', {
      name: 'https://gitlab.example.test/group/project/-/merge_requests/184',
    }),
  ).toBeVisible();
});
