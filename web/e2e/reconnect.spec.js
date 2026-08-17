// Incoming changes made by "another client" while this one was offline must
// appear in the already-open page when connectivity returns: reconnect is a
// full push-then-pull resync. The other client is a second server process on
// the same SQLite file (the two never run concurrently, so no contention).
import { expect, test } from './fixtures.js';
import { Server } from './server.js';
import { addTodo, editTitle, listTodos, row } from './helpers.js';

test('incoming changes sync on reconnect without a refresh', async ({ page, server }) => {
  // Show everything: the other client's completion must stay visible after
  // the pull (the default filter would hide it).
  await page.goto(`${server.base}/?filter=`);
  await addTodo(page, 'Remote edit');
  await expect(row(page, 'Remote edit')).toBeVisible();

  await server.stop();
  // A local change queues offline (must be pushed on reconnect)…
  await editTitle(page, 'Remote edit', 'Local rename');
  await expect(row(page, 'Local rename')).toBeVisible();

  // …while the other client completes the todo server-side.
  const other = new Server({ port: server.port + 1, dbPath: server.dbPath });
  await other.start();
  const remote = (await listTodos(other.base)).find((t) => t.title === 'Remote edit');
  const done = await fetch(`${other.base}/api/todos/${remote.id}/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ completed: true }),
  });
  expect(done.ok).toBe(true);
  await other.stop();

  await server.start();
  // Reconnect (the OS-level online event): push the queue, then pull the
  // server's state — the other client's completion must appear in the
  // already-open page without any manual refresh.
  await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await expect
    .poll(() => row(page, 'Local rename').locator('input[type=checkbox]').isChecked(), { timeout: 15000 })
    .toBe(true);
  await expect(row(page, 'Local rename')).not.toHaveClass(/pending/);
});
