// Lapsed session: rotating the API key invalidates every browser session
// (the cookie signing key is derived from it), so the open page's next
// event-stream connect fails with a 401 — the same thing an idle tab sees
// when its cookie expires. EventSource never retries a rejected connect, so
// the page must reload itself to mint a fresh cookie; without that reload
// the tab would sit dead forever.
import { serverTest, expect } from './fixtures.js';
import { addTodo, listTodos, row, updateTodo } from './helpers.js';

const KEY_A = 'e2e-key-a';
const KEY_B = 'e2e-key-b';

const test = serverTest({ apiKey: KEY_A });

test('rejected event stream reloads the page and live sync resumes', async ({ page, server }) => {
  await addTodo(page, 'Auth todo');
  await expect(row(page, 'Auth todo')).toBeVisible();

  await page.evaluate(() => {
    window.__sentinel = true;
  });
  await server.stop();
  server.apiKey = KEY_B;
  await server.start();

  // The sentinel property only disappears when a new document loads.
  await expect.poll(() => page.evaluate(() => !window.__sentinel), { timeout: 20000 }).toBe(true);

  // The reloaded page is fully functional again, live sync included.
  const target = (await listTodos(server.base, KEY_B)).find((t) => t.title === 'Auth todo');
  await updateTodo(server.base, target.id, { title: 'Auth renamed' }, KEY_B);
  await expect(row(page, 'Auth renamed')).toBeVisible();
});
