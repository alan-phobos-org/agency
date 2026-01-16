import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 120_000, // 2 min per test
  retries: 0,
  use: {
    baseURL: `https://localhost:${process.env.AG_WEB_PORT || 18443}`,
    ignoreHTTPSErrors: true, // Self-signed certs
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
});
