import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeCorrelationOTLPLogs,
  claudeCorrelationTranscriptNDJSON,
  claudeCorrelationTranscriptSessionID,
  claudeLiveCorrelationPromptID,
  fetchEventDetail,
  ingestClaudeTranscript,
  ingestOTLPLogs,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

const transcriptSessionId = `claude-code:${claudeCorrelationTranscriptSessionID}`;

// The live-data gate for the shared prompt correlation key: each surface is
// POSTed to the real daemon, then read back through the un-mocked read API and
// event inspector. Every listed event must expose the identical prompt id under
// provider_extensions.correlation, so a downstream consumer can group a session's
// events by prompt across OTLP (#106 prompt.id) and the session JSONL transcript
// (#263 promptId — on the prompt and on the tool_result-only record's tool call).
// `retained` names raw transcript fields that must also be served (#263).
const correlationSurfaces = [
  {
    surface: 'OTLP prompt.id (#106)',
    ingest: () => ingestOTLPLogs(claudeCorrelationOTLPLogs()),
    sessionId: 'claude-code:tiq-live-e2e-correlation',
    eventIds: ['1', '2'],
    retained: [] as Array<[string, string, string]>,
  },
  {
    surface: 'transcript promptId (#263)',
    ingest: () => ingestClaudeTranscript(claudeCorrelationTranscriptNDJSON()),
    sessionId: transcriptSessionId,
    eventIds: ['tiq-live-corr-user', 'toolcall:toolu_tiq_live_corr'],
    retained: [
      ['tiq-live-corr-user', 'transcript', 'permission_mode'],
      ['tiq-live-corr-user', 'transcript_record', 'turnPosition'],
      ['toolcall:toolu_tiq_live_corr', 'transcript_result', 'sourceToolAssistantUUID'],
    ] as Array<[string, string, string]>,
  },
];

for (const { surface, ingest, sessionId, eventIds, retained } of correlationSurfaces) {
  test(`retains the shared prompt correlation key on every live-ingested event: ${surface}`, async ({
    page,
  }) => {
    await ingest();

    for (const suffix of eventIds) {
      const eventId = `${sessionId}:${suffix}`;
      const detail = await fetchEventDetail(sessionId, eventId);
      expect(detail.event_id).toBe(eventId);
      const correlation = detail.provider_extensions.correlation as
        | { prompt_id?: string }
        | undefined;
      expect(correlation?.prompt_id).toBe(claudeLiveCorrelationPromptID);
    }
    for (const [suffix, block, key] of retained) {
      const detail = await fetchEventDetail(sessionId, `${sessionId}:${suffix}`);
      const extension = detail.provider_extensions[block] as Record<string, unknown> | undefined;
      expect(extension?.[key]).toBeDefined();
    }

    // The retained correlation id is also served through to the event inspector UI.
    const firstEventId = `${sessionId}:${eventIds[0]}`;
    await unlockDashboard(page, authToken);
    await page.goto(
      `/sessions/${encodeURIComponent(sessionId)}?event=${encodeURIComponent(firstEventId)}&inspector=attributes`,
    );
    await expect(page.locator('#event-inspector')).toBeVisible();
    await expect(page.locator('#event-inspector')).toContainText('Provider extensions');
    await expect(page.locator('#event-inspector')).toContainText(claudeLiveCorrelationPromptID);
  });
}
