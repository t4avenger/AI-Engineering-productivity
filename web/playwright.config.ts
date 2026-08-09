import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:4173',
    trace: 'on-first-retry',
  },
  webServer: [
    {
      command:
        'cd .. && TELEMETRYIQ_AUTH_TOKEN=playwright-token TELEMETRYIQ_PORT=18080 go run ./cmd/telemetryiq',
      url: 'http://127.0.0.1:18080/api/v1/health',
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command:
        'VITE_API_URL=http://127.0.0.1:18080/api/v1 VITE_HEALTH_URL=http://127.0.0.1:18080/api/v1/health npm run build && npm run preview -- --port 4173',
      url: 'http://127.0.0.1:4173',
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
