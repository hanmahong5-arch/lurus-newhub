import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright config for the console (web/next).
 * Default host = the UAT instance; override for a local stack:
 *   E2E_BASE_URL=http://localhost:5173 (rsbuild dev server, proxies the API).
 *
 * Auth model: a single `setup` project logs in ONCE via the bridge and saves
 * storageState (session cookie + the console's language preference). All specs
 * reuse it - the bridge is rate-limited to 5 logins/60s (BootstrapRateLimit),
 * so a per-test login would 429.
 *
 * Env vars: E2E_BASE_URL, E2E_BRIDGE_TOKEN, E2E_TENANT_SLUG, E2E_USER_ID,
 * E2E_BACKEND_URL (relay origin when it differs from the console origin).
 *
 * Not in PR-blocking CI; runs nightly / locally before merging.
 */

const baseURL = process.env.E2E_BASE_URL || 'https://test-newhub.lurus.cn'

export const STORAGE_STATE = 'e2e/.auth/state.json'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report' }],
    ['json', { outputFile: 'playwright-report/results.json' }],
  ],
  use: {
    baseURL,
    // The console ships English and Chinese; specs assert the English strings
    // and pin the preference in storageState (see e2e/helpers/auth.ts).
    locale: 'en-US',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'setup',
      testMatch: /global\.setup\.ts/,
    },
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], storageState: STORAGE_STATE },
      dependencies: ['setup'],
    },
  ],
})
