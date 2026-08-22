// Detail page (/todo/<id>): context-menu entry, deep links, back/forward,
// the complete toggle, hierarchy navigation, and the not-found state.
import { expect, test } from './fixtures.js';
import { addTodo, addTodoApi, boards, listTodos, row } from './helpers.js';

// Create a todo with arbitrary fields straight through the API, acting as
// another client would.
async function create(base, boardId, payload) {
  const res = await fetch(`${base}/api/todos`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ boardId, ...payload }),
  });
  if (!res.ok) throw new Error(`create todo: ${res.status}`);
  return res.json();
}

async function openDetails(page, title) {
  await row(page, title).click({ button: 'right' });
  await page.locator('.context-menu').getByRole('menuitem', { name: 'Details' }).click();
}

test('context menu opens the detail page with every field', async ({ page, server }) => {
  const boardId = await server.ensureBoard();
  const todo = await create(server.base, boardId, {
    title: 'Detailled todo',
    description: 'A **bold** statement',
    labels: ['spec-label'],
    priority: 'high',
    dueDate: '2026-09-01',
  });

  await expect(row(page, 'Detailled todo')).toBeVisible();
  await openDetails(page, 'Detailled todo');
  await page.waitForURL(`**/todo/${todo.id}`);

  await expect(page.locator('.detail-page h2.title')).toHaveText('Detailled todo');
  // Markdown description renders as real elements, not raw text.
  await expect(page.locator('.description strong')).toHaveText('bold');
  await expect(page.locator('.facts')).toContainText('spec-label');
  await expect(page.locator('.facts')).toContainText('high');
  await expect(page.locator('.facts')).toContainText('2026');
  // createdAt arrives from the server; the fact row shows a local timestamp.
  await expect(page.locator('.facts')).toContainText('Created');
  const boardName = (await boards(server.base)).find((b) => b.id === boardId)?.name ?? '';
  await expect(page.locator('.facts')).toContainText(boardName);
});

test('detail page is deep-linkable and Back returns to the list', async ({ page, server }) => {
  const boardId = await server.ensureBoard();
  const todo = await addTodoApi(server.base, boardId, 'Deep linked');

  await page.goto(`${server.base}/todo/${todo.id}`);
  await expect(page.locator('.detail-page h2.title')).toHaveText('Deep linked');

  // A cold deep link has no in-app history to return to, so Back pushes the
  // todo's board list instead.
  await page.getByRole('button', { name: 'Back' }).click();
  await expect(page).toHaveURL((url) => url.searchParams.get('board') === String(boardId));
  await expect(page.locator('input[placeholder="Add a top-level todo…"]')).toBeVisible();
});

test('completing from the detail page reaches the server and stamps completedAt', async ({ page, server }) => {
  const boardId = await server.ensureBoard();
  const todo = await addTodoApi(server.base, boardId, 'Toggle me');

  await page.goto(`${server.base}/todo/${todo.id}`);
  await page.locator('.card input[type=checkbox]').check();

  await expect(page.locator('.facts')).toContainText('Completed');
  const list = await listTodos(server.base);
  const done = list.find((t) => t.id === todo.id);
  expect(done?.completed).toBe(true);
  expect(done?.completedAt).toBeTruthy();
});

test('browser back and forward switch between list and detail', async ({ page, server }) => {
  await addTodo(page, 'History todo');
  await expect(row(page, 'History todo')).toBeVisible();

  await openDetails(page, 'History todo');
  await page.waitForURL(/\/todo\/\d+$/);
  await page.goBack();
  await expect(page.locator('input[placeholder="Add a top-level todo…"]')).toBeVisible();
  await expect(row(page, 'History todo')).toBeVisible();
  await page.goForward();
  await expect(page.locator('.detail-page h2.title')).toHaveText('History todo');
});

test('subtasks and the parent breadcrumb navigate between details', async ({ page, server }) => {
  const boardId = await server.ensureBoard();
  const parent = await addTodoApi(server.base, boardId, 'Parent todo');
  const child = await create(server.base, boardId, { title: 'Child todo', parentId: parent.id });

  await page.goto(`${server.base}/todo/${parent.id}`);
  await expect(page.locator('.subtasks')).toContainText('Child todo');

  await page.locator('.subtask', { hasText: 'Child todo' }).click();
  await page.waitForURL(`**/todo/${child.id}`);
  await expect(page.locator('.breadcrumb')).toContainText('Parent todo');

  await page.locator('.crumb', { hasText: 'Parent todo' }).click();
  await page.waitForURL(`**/todo/${parent.id}`);
});

test('unknown ids show the not-found state', async ({ page, server }) => {
  await page.goto(`${server.base}/todo/999999`);
  await expect(page.locator('.detail-page')).toContainText('Todo not found');
  await page.getByRole('button', { name: 'Back to list' }).click();
  await expect(page.locator('input[placeholder="Add a top-level todo…"]')).toBeVisible();
});

test('Details behaves as a real link: middle and ctrl clicks open a new tab', async ({ page, context, server }) => {
  const boardId = await server.ensureBoard();
  const todo = await addTodoApi(server.base, boardId, 'Linked todo');
  await expect(row(page, 'Linked todo')).toBeVisible();

  await row(page, 'Linked todo').click({ button: 'right' });
  const details = page.locator('.context-menu').getByRole('menuitem', { name: 'Details' });
  // A real href is what makes middle-click, "open in new tab", and "copy
  // link address" work natively.
  await expect(details).toHaveAttribute('href', `/todo/${todo.id}`);

  const middle = context.waitForEvent('page');
  await details.click({ button: 'middle' });
  const middleTab = await middle;
  await middleTab.waitForURL(`**/todo/${todo.id}`);
  await expect(middleTab.locator('.detail-page h2.title')).toHaveText('Linked todo');

  // The original tab dismissed its menu and stayed on the list.
  await expect(page.locator('.context-menu')).toHaveCount(0);
  await expect(page.locator('input[placeholder="Add a top-level todo…"]')).toBeVisible();

  // Ctrl+click keeps its browser default too (new background tab).
  await row(page, 'Linked todo').click({ button: 'right' });
  const ctrl = context.waitForEvent('page');
  await details.click({ modifiers: ['Control'] });
  const ctrlTab = await ctrl;
  await ctrlTab.waitForURL(`**/todo/${todo.id}`);
  await expect(ctrlTab.locator('.detail-page h2.title')).toHaveText('Linked todo');
});

test('another client completing the todo updates the open page', async ({ page, server }) => {
  const boardId = await server.ensureBoard();
  const todo = await addTodoApi(server.base, boardId, 'Live synced');

  await page.goto(`${server.base}/todo/${todo.id}`);
  await expect(page.locator('.detail-page h2.title')).toHaveText('Live synced');
  await expect(page.locator('.card input[type=checkbox]')).not.toBeChecked();

  // Another client completes it; the SSE poke refreshes the open page.
  await fetch(`${server.base}/api/todos/${todo.id}/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ completed: true }),
  });
  await expect(page.locator('.card input[type=checkbox]')).toBeChecked();
  await expect(page.locator('.facts')).toContainText('Completed');
});
