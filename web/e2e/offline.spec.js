// Offline queue through the real UI, with offline simulated by actually
// stopping the server process — the service worker's network fetches then
// genuinely fail, which DevTools-style offline emulation can miss since it
// does not always cover worker traffic.
import { expect, test } from './fixtures.js';
import { addTodo, badgeText, listTodos, nudgeSync, row } from './helpers.js';

test('offline create + complete survive a reload and sync on reconnect', async ({ page, server }) => {
  // Show everything for this test (the default filter hides completed todos).
  await page.goto(`${server.base}/?filter=`);
  await addTodo(page, 'Existing todo');
  await expect(row(page, 'Existing todo')).toBeVisible();

  await server.stop();
  await addTodo(page, 'Offline task');
  await expect(row(page, 'Offline task')).toBeVisible();
  await expect(row(page, 'Offline task')).toHaveClass(/pending/);
  await expect.poll(() => badgeText(page)).toContain('Offline');

  await row(page, 'Existing todo').locator('input[type=checkbox]').check();
  await expect(row(page, 'Existing todo')).toHaveClass(/pending/);
  await expect(row(page, 'Existing todo').locator('input[type=checkbox]')).toBeChecked();

  // Reload while offline: the shell and list come from the worker cache and
  // the queue from localStorage, so the projection survives.
  await page.reload();
  await expect(row(page, 'Offline task')).toBeVisible();
  await expect(row(page, 'Offline task')).toHaveClass(/pending/);

  await server.start();
  await nudgeSync(page);
  await expect(row(page, 'Offline task')).not.toHaveClass(/pending/);

  const list = await listTodos(server.base);
  expect(list.find((t) => t.title === 'Offline task')).toBeTruthy();
  expect(list.find((t) => t.title === 'Existing todo')?.completed).toBe(true);
  await expect.poll(() => badgeText(page)).toBe(null);
});
