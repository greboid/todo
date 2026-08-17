// Baseline: completing a todo through the real UI round-trips to the server.
import { expect, test } from './fixtures.js';
import { addTodo, listTodos, row } from './helpers.js';

test('online completion reaches the server', async ({ page, server }) => {
  await addTodo(page, 'Online todo');
  await expect(row(page, 'Online todo')).toBeVisible();
  await row(page, 'Online todo').locator('input[type=checkbox]').check();
  // Default filter hides completed todos.
  await expect(row(page, 'Online todo')).toBeHidden();
  const list = await listTodos(server.base);
  const done = list.find((t) => t.title === 'Online todo');
  expect(done?.completed).toBe(true);
});
