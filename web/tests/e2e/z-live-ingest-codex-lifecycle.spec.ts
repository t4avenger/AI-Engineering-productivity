import { expect, test } from '@playwright/test';

import {
  authToken,
  codexLifecycleOTLPLogs,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('renders Codex lifecycle signals ingested through the live daemon', async ({
  page,
}) => {
  await ingestOTLPLogs(codexLifecycleOTLPLogs());

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
  expect(session?.state).toBe('active');
  expect(session?.completed_at ?? null).toBeNull();
  expect(session?.attributes.entrypoint).toBe('codex exec');
  expect(session?.attributes.service_name).toBe('codex_exec');
  expect(session?.attributes.service_version).toBe('0.153.4');
  expect(session?.availability.entrypoint).toBe('observed');
  expect(session?.availability.tool_version).toBe('observed');
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

  await unlockDashboard(page, authToken);
  await page.goto('/sessions/codex:tiq-live-e2e-lifecycle-session');
  await expect(page.getByRole('heading', { name: 'Environment' })).toBeVisible();
  await expect(page.getByText('codex_exec')).toBeVisible();
  await expect(page.getByText('0.153.4')).toBeVisible();
  await expect(page.getByText('Session active')).toBeVisible();
  await expect(page.getByText('Lifecycle').first()).toBeVisible();
  await expect(page.getByText('session_start')).toBeVisible();
  await expect(page.getByText('codex exec').first()).toBeVisible();
  await expect(page.getByText('tiq-canary-live-lifecycle')).toHaveCount(0);
  await expect(page.getByText('lifecycle-live@example.test')).toHaveCount(0);
});
