import { defineConfig } from '@playwright/test';

// The suite drives the *production* artifact: global-setup runs `go build`
// against the frontend embedded in internal/ui/dist, so rebuild the frontend
// first (pnpm --dir web run build) whenever it changed.
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list']],
  globalSetup: './e2e/global-setup.js',
  use: {
    trace: 'retain-on-failure',
  },
});
