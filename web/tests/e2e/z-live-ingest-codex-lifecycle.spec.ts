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
    data: Array<{ session_id: string; state: string; completed_at?: string | null }>;
  };
  expect(
    sessionBody.data.some(
      (session) =>
        session.session_id === 'codex:tiq-live-e2e-lifecycle-session' &&
        session.state === 'active' &&
        session.completed_at == null,
    ),
  ).toBe(true);

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
  await expect(page.getByText('Session active')).toBeVisible();
  await expect(page.getByText('Lifecycle').first()).toBeVisible();
  await expect(page.getByText('session_start')).toBeVisible();
  await expect(page.getByText('codex exec').first()).toBeVisible();
  await expect(page.getByText('tiq-canary-live-lifecycle')).toHaveCount(0);
  await expect(page.getByText('lifecycle-live@example.test')).toHaveCount(0);
});
