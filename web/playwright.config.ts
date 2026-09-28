import { defineConfig, devices } from '@playwright/test';

const testConfigHome = `/tmp/telemetryiq-playwright-${String(process.pid)}`;
const testPort = process.env.TELEMETRYIQ_E2E_PORT ?? '18080';
const testOrigin = `http://localhost:${testPort}`;

export default defineConfig({
  updateSnapshots:
    process.env.TELEMETRYIQ_UPDATE_REFERENCE_SNAPSHOTS === '1' ? 'all' : 'none',
  testDir: './tests/e2e',
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: testOrigin,
    trace: 'on-first-retry',
  },
  webServer: [
    {
      command: `cd .. && XDG_CONFIG_HOME=${testConfigHome} TELEMETRYIQ_AUTH_TOKEN=playwright-token TELEMETRYIQ_HOST=localhost TELEMETRYIQ_PORT=${testPort} go run ./cmd/telemetryiq`,
      url: `${testOrigin}/api/v1/health`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
