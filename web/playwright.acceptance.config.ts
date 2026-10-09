import { defineConfig, devices } from '@playwright/test';

/**
 * Real-chain acceptance (tests/acceptance). Not the e2e suite: these run
 * against an instance whose upstream is the fake vendor and whose platform is
 * the fake wallet (cmd/fakeupstream), and every money assertion is exact.
 *
 * Started by scripts/acceptance-stack.sh, which builds and boots the stack and
 * exports ACCEPT_*; running this config without them fails at once instead of
 * skipping (an empty run must never read as a pass — see
 * scripts/acceptance-meta-gate.mjs).
 *
 * No retries: a money scenario that passes on the second try has found a
 * race, and that is a failure.
 */

const baseURL = process.env.ACCEPT_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ACCEPT_BASE_URL is not set. Run `bash scripts/acceptance-stack.sh`, or source web/acceptance-report/stack.env from an ACCEPT_UP_ONLY=1 stack.',
  );
}

export default defineConfig({
  testDir: './tests/acceptance',
  testMatch: /.*\.spec\.ts$/,
  globalSetup: './tests/acceptance/fixtures/world.setup.ts',
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  timeout: 120_000,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'acceptance-report/html' }],
    ['json', { outputFile: 'acceptance-report/results.json' }],
  ],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // A machine that cannot download this Playwright's browser can point at
    // a Chromium it already has. Unset everywhere else, CI included.
    launchOptions: process.env.ACCEPT_CHROMIUM
      ? { executablePath: process.env.ACCEPT_CHROMIUM }
      : {},
  },
  projects: [{ name: 'acceptance', use: { ...devices['Desktop Chrome'] } }],
});
