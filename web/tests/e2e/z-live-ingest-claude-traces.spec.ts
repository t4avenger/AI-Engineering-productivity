import { type Page, expect, test } from '@playwright/test';

import {
  authToken,
  claudeContentOTLPLogs,
  claudeInteractionOTLPTraces,
  expectSessionDetailHeading,
  expectSessionTraceLanes,
  fetchLiveSessions,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

/**
 * Live end-to-end gate for #210: Claude trace↔log↔conversation session
 * correlation. Both tests drive the real daemon (no page.route mocking).
 * The joined test proves trace spans and content logs sharing one session.id
 * reconstruct one provider session whose Session Trace shows both the
 * Conversation and Spans lanes populated. The unavailable test proves a
 * trace with no session.id stays a trace-scoped observation whose conversation
 * coverage is unavailable — never merged by proximity.
 */
resetDaemonBetweenTests();

// openSessionTrace unlocks the dashboard and opens the Session Trace for one
// session, asserting the detail heading and the five lanes render. Shared by
// both tests so the nav/unlock/lane assertions live in exactly one place.
async function openSessionTrace(page: Page, sessionId: string): Promise<void> {
  await unlockDashboard(page, authToken);
  await page.goto(`/sessions/${encodeURIComponent(sessionId)}`);
  await expectSessionDetailHeading(page);
  await expectSessionTraceLanes(page);
}

test('joins Claude trace spans and content logs on a shared session.id', async ({
  page,
}) => {
  const sessionId = 'tiq-live-e2e-claude-corr';
  await ingestOTLPLogs(claudeContentOTLPLogs(sessionId));
  await ingestOTLPTraces(
    claudeInteractionOTLPTraces({
      traceId: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      spanId: 'aaaaaaaaaaaaaaaa',
      sessionId,
    }),
  );

  await expect
    .poll(async () => fetchLiveSessions())
    .toContainEqual(
      expect.objectContaining({
        session_id: 'claude-code:tiq-live-e2e-claude-corr',
        identity_scope: 'provider',
        identity_source: 'session.id',
        availability: expect.objectContaining({ conversation: 'observed' }),
      }),
    );

  await openSessionTrace(page, 'claude-code:tiq-live-e2e-claude-corr');
  await expect(page.getByLabel('Conversation lane')).toContainText(
    'tiq-live-e2e retained user',
  );
  await expect(page.getByLabel('Spans lane')).toBeVisible();
  await expect(page.getByLabel('Spans lane')).not.toContainText(
    'No retained trace span evidence',
  );
});

test('keeps a Claude trace with no session.id a conversation-unavailable observation', async ({
  page,
}) => {
  await ingestOTLPTraces(
    claudeInteractionOTLPTraces({
      traceId: 'cccccccccccccccccccccccccccccccc',
      spanId: 'cccccccccccccccc',
    }),
  );

  const observationID = 'claude-code:trace:cccccccccccccccccccccccccccccccc';

  const observations = await fetch(
    'http://localhost:18080/api/v1/sessions?scope=observation&limit=10',
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(observations.status).toBe(200);
  const body = (await observations.json()) as {
    data: Array<{
      session_id: string;
      identity_scope: string;
      identity_source: string;
      availability: Record<string, string>;
    }>;
  };
  expect(body.data).toEqual([
    expect.objectContaining({
      session_id: observationID,
      identity_scope: 'observation',
      identity_source: 'trace.id',
      availability: expect.objectContaining({ conversation: 'unavailable' }),
    }),
  ]);

  await openSessionTrace(page, observationID);
  await expect(page.getByLabel('Conversation lane')).toContainText(
    'No retained conversation evidence for this session.',
  );
  await expect(page.getByLabel('Spans lane')).not.toContainText(
    'No retained trace span evidence',
  );
});
