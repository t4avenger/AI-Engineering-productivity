import { defineConfig, devices } from '@playwright/test';

const testConfigHome = `/tmp/telemetryiq-playwright-${String(process.pid)}`;

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:18080',
    trace: 'on-first-retry',
  },
  webServer: [
    {
      command: `cd .. && XDG_CONFIG_HOME=${testConfigHome} TELEMETRYIQ_AUTH_TOKEN=playwright-token TELEMETRYIQ_HOST=localhost TELEMETRYIQ_PORT=18080 go run ./cmd/telemetryiq`,
      url: 'http://localhost:18080/api/v1/health',
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
