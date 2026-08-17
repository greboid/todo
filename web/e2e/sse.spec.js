// SSE live sync: pokes from foreign mutations reach an idle page with no
// interaction at all, the stream survives a server restart, and two tabs
// share it without accumulating aborted event-stream requests.
import { expect, test } from './fixtures.js';
import { addTodoApi, boards, row } from './helpers.js';

test('foreign mutation appears without interaction', async ({ page, server }) => {
  const boardId = (await boards(server.base))[0].id;
  await addTodoApi(server.base, boardId, 'Poked todo');
  await expect(row(page, 'Poked todo')).toBeVisible();
});

test('pokes resume after a server restart without a refresh', async ({ page, server }) => {
  // Restarting the server drops every SSE stream. A foreign mutation made
  // afterwards must still reach the already-open page: the stream reconnects
  // (native EventSource retry) and the reconnection itself re-fetches,
  // covering a poke that raced the reconnect.
  await server.stop();
  await server.start();
  const boardId = (await boards(server.base))[0].id;
  await addTodoApi(server.base, boardId, 'After restart');
  await expect(row(page, 'After restart')).toBeVisible({ timeout: 20000 });
});

test('both tabs receive the poke with no aborted event streams', async ({ page, server, context }) => {
  const page2 = await context.newPage();
  // Armed after setup: a page load inherently aborts its in-flight event
  // stream (the browser cancels it on navigation); only aborts after the
  // tab is settled count as regressions.
  let armed = false;
  let aborted = 0;
  page2.on('requestfailed', (req) => {
    if (armed && new URL(req.url()).pathname === '/api/events') aborted++;
  });
  await page2.goto(server.base);
  await expect(page2.getByPlaceholder('Add a top-level todo…')).toBeVisible();
  armed = true;

  const boardId = (await boards(server.base))[0].id;
  await addTodoApi(server.base, boardId, 'Second tab poke');
  await expect(row(page, 'Second tab poke')).toBeVisible();
  await expect(row(page2, 'Second tab poke')).toBeVisible();
  await page.waitForTimeout(1000);
  expect(aborted).toBe(0);
  await page2.close();
});
