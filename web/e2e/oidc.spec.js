// OpenID Connect: the server runs against a fake provider (oidc-provider.js)
// that approves every login at once. Page loads without a session go through
// a login round trip and land back on the app; API clients authenticate with
// access tokens; and when a session lapses under a page the service worker
// controls, the page's reload goes back through the provider and recovers.
// An API key is configured alongside, which also checks the two coexist (the
// server fixture uses it for readiness and the API helpers).
import { test as base, expect } from '@playwright/test';
import { Provider } from './oidc-provider.js';
import { Server } from './server.js';
import { addTodo, listTodos, row } from './helpers.js';

const KEY = 'e2e-oidc-key';
const CLIENT_ID = 'e2e-todo';
const CLIENT_SECRET = 'e2e-secret';
const AUDIENCE = 'https://todo.e2e.example';

let portEpoch = 0;

const test = base.extend({
  provider: async ({}, use) => {
    const provider = new Provider({ clientId: CLIENT_ID, clientSecret: CLIENT_SECRET });
    await provider.start();
    await use(provider);
    await provider.stop();
  },
  server: async ({ provider }, use) => {
    const worker = Number(process.env.TEST_WORKER_INDEX ?? 0);
    const port = 30000 + worker * 100 + (portEpoch++ % 40);
    const server = new Server({
      port,
      apiKey: KEY,
      env: {
        TODO_OIDC_ISSUER: provider.issuer,
        TODO_OIDC_CLIENT_ID: CLIENT_ID,
        TODO_OIDC_CLIENT_SECRET: CLIENT_SECRET,
        TODO_OIDC_SESSION_KEY: 'session-key-a',
        TODO_OIDC_AUDIENCE: AUDIENCE,
      },
    });
    await server.start();
    await server.ensureBoard();
    await use(server);
    await server.stop();
    await server.cleanup();
  },
});

const input = (page) => page.locator('input[placeholder="Add a top-level todo…"]');

test('a page load logs in through the provider and lands back on the app', async ({ page, server, provider }) => {
  await page.goto(`${server.base}/?from=e2e`);
  await expect(input(page)).toBeVisible();
  expect(provider.logins).toBe(1);
  expect(page.url()).toBe(`${server.base}/?from=e2e`);

  await addTodo(page, 'OIDC todo');
  await expect(row(page, 'OIDC todo')).toBeVisible();
  expect((await listTodos(server.base, KEY)).map((t) => t.title)).toContain('OIDC todo');

  // The session now carries further page loads without another login.
  await page.reload();
  await expect(row(page, 'OIDC todo')).toBeVisible();
  expect(provider.logins).toBe(1);
});

test('API clients authenticate with access tokens', async ({ server, provider }) => {
  const get = (headers) => fetch(`${server.base}/api/boards`, { headers }).then((r) => r.status);
  expect(await get({ Authorization: `Bearer ${provider.accessToken(AUDIENCE)}` })).toBe(200);
  expect(await get({ Authorization: `Bearer ${provider.accessToken('http://elsewhere.example')}` })).toBe(401);
  expect(await get({ Authorization: `Bearer ${KEY}` })).toBe(200);
  expect(await get({})).toBe(401);
});

test('a lapsed session under the service worker logs in again', async ({ page, server, provider }) => {
  await page.goto(server.base);
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();
  await expect(input(page)).toBeVisible();
  expect(await page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);
  const before = provider.logins;

  await page.evaluate(() => {
    window.__sentinel = true;
  });
  // A new session key invalidates the page's cookie, as an idle expiry would.
  await server.stop();
  server.env = { ...server.env, TODO_OIDC_SESSION_KEY: 'session-key-b' };
  await server.start();

  await expect.poll(() => page.evaluate(() => !window.__sentinel), { timeout: 20000 }).toBe(true);
  await expect(input(page)).toBeVisible();
  expect(provider.logins).toBe(before + 1);

  await addTodo(page, 'After relogin');
  await expect(row(page, 'After relogin')).toBeVisible();
  expect((await listTodos(server.base, KEY)).map((t) => t.title)).toContain('After relogin');
});
