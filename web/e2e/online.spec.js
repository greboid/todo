// Online UI round-trips and server-ordered rendering.
import { expect, test } from './fixtures.js';
import { addTodo, addTodoApi, createBoard, listTodos, row, updateTodo } from './helpers.js';

test('renders the API sort order for roots and children', async ({ page, server }) => {
  const { id: boardId } = await createBoard(server.base, 'Sort order');
  const late = await addTodoApi(server.base, boardId, 'Late root');
  const early = await addTodoApi(server.base, boardId, 'Early root');
  const lateChild = await addTodoApi(server.base, boardId, 'Late child');
  const earlyChild = await addTodoApi(server.base, boardId, 'Early child');
  await updateTodo(server.base, late.id, { dueDate: '2026-09-02' });
  await updateTodo(server.base, early.id, { dueDate: '2026-09-01' });
  await updateTodo(server.base, lateChild.id, { parentId: early.id, dueDate: '2026-09-02' });
  await updateTodo(server.base, earlyChild.id, { parentId: early.id, dueDate: '2026-09-01' });

  await page.goto(`${server.base}/?board=${boardId}&filter=sort:date`);
  await expect(page.locator('.item .title')).toHaveText(['Early root', 'Early child', 'Late child', 'Late root']);
  await page.goto(`${server.base}/?board=${boardId}&filter=sort:!date`);
  await expect(page.locator('.item .title')).toHaveText(['Late root', 'Early root', 'Late child', 'Early child']);
});

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
