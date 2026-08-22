// API and page helpers shared by the e2e specs. The API helpers talk
// straight to the Go server (acting as "another client"); the page helpers
// drive the real UI the way a user would.

export function authHeaders(key = '') {
  return key ? { 'X-API-Key': key } : {};
}

async function jsonOrThrow(res, what) {
  if (!res.ok) throw new Error(`${what}: ${res.status}`);
  return res.json();
}

export async function boards(base, key = '') {
  return jsonOrThrow(await fetch(`${base}/api/boards`, { headers: authHeaders(key) }), 'list boards');
}

export async function createBoard(base, name, key = '') {
  return jsonOrThrow(
    await fetch(`${base}/api/boards`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders(key) },
      body: JSON.stringify({ name }),
    }),
    'create board',
  );
}

export async function listTodos(base, key = '', filter = '') {
  const q = filter ? `?filter=${encodeURIComponent(filter)}` : '';
  return jsonOrThrow(await fetch(`${base}/api/todos${q}`, { headers: authHeaders(key) }), 'list todos');
}

export async function updateTodo(base, id, patch, key = '') {
  return jsonOrThrow(
    await fetch(`${base}/api/todos/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', ...authHeaders(key) },
      body: JSON.stringify(patch),
    }),
    'update todo',
  );
}

export async function addTodoApi(base, boardId, title, key = '') {
  return jsonOrThrow(
    await fetch(`${base}/api/todos`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders(key) },
      body: JSON.stringify({ title, boardId }),
    }),
    `api add "${title}"`,
  );
}

// --- page helpers ---

export async function addTodo(page, text) {
  await page.fill('input[placeholder="Add a top-level todo…"]', text);
  await page.press('input[placeholder="Add a top-level todo…"]', 'Enter');
}

export function row(page, title) {
  return page.locator('.item').filter({ has: page.locator('.title', { hasText: title }) }).first();
}

export async function editTitle(page, from, to) {
  await row(page, from).locator('.title').dblclick();
  // While editing, the row's title span is replaced by the form, so scope to
  // the (globally unique, single-edit) form instead of the row.
  const form = page.locator('form.edit');
  await form.locator('input[type=text]').first().fill(to);
  await form.locator('button[type=submit]').click();
}

export async function nudgeSync(page) {
  // Focus and visibility re-attempt immediately; either unblocks a stalled
  // flush without waiting for the backoff timer.
  await page.evaluate(() => {
    window.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

// The badge's text, or null when it is not rendered. count() is instant, so
// the absent (healthy) case doesn't burn a locator timeout waiting for an
// element that is never going to appear.
export async function badgeText(page) {
  const badge = page.locator('.sync');
  if (!(await badge.count())) return null;
  return (await badge.textContent())?.trim() ?? null;
}
