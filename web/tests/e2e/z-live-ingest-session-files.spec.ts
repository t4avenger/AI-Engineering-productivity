import { expect, test } from '@playwright/test';

import {
  authToken,
  claudeToolSpanFilePathOTLPTraces,
  codexFilesystemWriteOTLPLogs,
  ingestOTLPLogs,
  ingestOTLPTraces,
  resetDaemonBetweenTests,
  unlockDashboard,
} from './live-ingest-helpers';

resetDaemonBetweenTests();

test('projects file evidence from live Claude path and Codex write ingest', async ({
  page,
}) => {
  await ingestOTLPTraces(claudeToolSpanFilePathOTLPTraces());
  await ingestOTLPLogs(codexFilesystemWriteOTLPLogs());

  const claudeSession = 'claude-code:tiq-live-e2e-session-files';
  const files = await fetch(
    `http://localhost:18080/api/v1/sessions/${encodeURIComponent(claudeSession)}/files`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(files.status).toBe(200);
  const body = (await files.json()) as {
    data: Array<{
      path: string | null;
      action: string | null;
      availability: { path: string; line_diff: string };
    }>;
  };
  expect(body.data.length).toBeGreaterThanOrEqual(1);
  expect(body.data[0]).toEqual(
    expect.objectContaining({
      path: '/workspace/tiq-live-e2e-session-files.go',
      action: 'read',
      availability: expect.objectContaining({
        path: 'observed',
        line_diff: 'unavailable',
      }),
    }),
  );

  const codexSession = 'codex:tiq-live-e2e-session-files-codex';
  const writeFiles = await fetch(
    `http://localhost:18080/api/v1/sessions/${encodeURIComponent(codexSession)}/files`,
    { headers: { Authorization: `Bearer ${authToken}` } },
  );
  expect(writeFiles.status).toBe(200);
  const writeBody = (await writeFiles.json()) as {
    data: Array<{
      path: string | null;
      action: string | null;
      availability: { path: string };
    }>;
  };
  expect(writeBody.data).toEqual([
    expect.objectContaining({
      path: null,
      action: 'write',
      availability: expect.objectContaining({ path: 'unavailable' }),
    }),
  ]);

  await unlockDashboard(page, authToken);
  await page.goto(`/sessions/${encodeURIComponent(claudeSession)}`);
  const evidence = page.getByLabel('File evidence');
  await expect(page.getByRole('heading', { name: 'File evidence' })).toBeVisible();
  await expect(evidence.getByText('/workspace/tiq-live-e2e-session-files.go')).toBeVisible();
  await expect(evidence.getByText(/Action\s+read/)).toBeVisible();
  await expect(page.getByText('tiq-canary-live-session-files')).toHaveCount(0);
});
