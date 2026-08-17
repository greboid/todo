// Shared fixtures for the Playwright suite. Every worker owns a throwaway Go
// server (fresh temp SQLite database, unique port derived from the worker
// index); every test gets a fresh page that is already controlled by the
// service worker (installed via the goto/ready/reload dance the app needs
// before offline behaviour works) on a board that exists. Tests are
// self-contained, so files and tests run fully parallel; specs may stop and
// restart their server mid-test to simulate going offline.
import { test as base, expect } from '@playwright/test';
import { Server } from './server.js';

if (!process.env.E2E_BIN) {
  throw new Error('E2E_BIN is not set — did the playwright globalSetup run?');
}

// Per-process epoch so a worker that runs several spec files sequentially
// never reuses a port whose server may still be tearing down.
let portEpoch = 0;

// serverTest() builds a `test` whose fixtures drive a Server; apiKey guards
// the whole API (the session spec rotates it mid-test by mutating the
// instance and restarting).
export function serverTest({ apiKey = '' } = {}) {
  return base.extend({
    server: [
      async ({}, use) => {
        const worker = Number(process.env.TEST_WORKER_INDEX ?? 0);
        const port = 20000 + worker * 100 + (portEpoch++ % 40) * 2;
        const server = new Server({ port, apiKey });
        await server.start();
        await use(server);
        await server.stop();
        await server.cleanup();
      },
      { scope: 'worker' },
    ],
    page: async ({ context, server }, use) => {
      await server.ensureBoard();
      const page = await context.newPage();
      await page.goto(server.base);
      // Let the service worker install, then reload so it controls the page.
      await page.evaluate(() => navigator.serviceWorker.ready);
      await page.reload();
      await use(page);
    },
  });
}

export const test = serverTest();
export { expect };
