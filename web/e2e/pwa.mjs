// PWA e2e tests: drives real Chromium (Playwright) against a throwaway
// Go server with a temp database. Offline is simulated by actually stopping
// the server process — the service worker's network fetches then genuinely
// fail, which is exactly what the app must handle (and what DevTools-style
// offline emulation can miss, since it does not always cover worker
// traffic).
//
// Run after a frontend build: pnpm --dir web run e2e
// (go build happens here; internal/ui/dist must be current).
//
// Scenarios:
//   1. online completion works end-to-end
//   2. offline: queued create + complete project optimistically (dashed
//      outline), survive a reload, and sync on reconnect
//   3. wedged navigator.onLine: flushes still run and self-heal
//   4. merge dialog: clashing server-side edit when back online, both
//      resolutions, plus the Decide-later deferral path
//   5. offline deletion of a todo created offline cancels its intents

import { execFile, spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { chromium } from 'playwright';

const root = path.resolve(import.meta.dirname, '../..');
const run = promisify(execFile);

let failures = 0;

function check(name, ok) {
  const mark = ok ? 'ok' : 'FAIL';
  console.log(`${mark}   ${name}`);
  if (!ok) failures++;
}

// Poll until fn() returns truthy (or the timeout kills the run).
async function until(fn, { timeout = 10000, interval = 100, what = 'condition' } = {}) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) {
      last = e;
    }
    await new Promise((r) => setTimeout(r, interval));
  }
  throw new Error(`timed out waiting for ${what}; last: ${last}`);
}

class Server {
  constructor(bin, cwd, port, dbPath) {
    this.bin = bin;
    this.cwd = cwd;
    this.port = port;
    this.dbPath = dbPath;
  }

  get base() {
    return `http://127.0.0.1:${this.port}`;
  }

  async start() {
    this.proc = spawn(this.bin, [], {
      cwd: this.cwd,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, TODO_ADDR: `127.0.0.1:${this.port}`, TODO_DB: this.dbPath, TODO_DB_DRIVER: 'sqlite' },
    });
    const exited = once(this.proc, 'exit').then(() => {});
    this.exited = exited;
    await until(
      async () => {
        const res = await fetch(`${this.base}/api/boards`).catch(() => null);
        return res && res.ok;
      },
      { what: 'server readiness', timeout: 15000 },
    );
  }

  async stop() {
    if (!this.proc || this.proc.exitCode != null) return;
    this.proc.kill('SIGTERM');
    await Promise.race([this.exited, new Promise((r) => setTimeout(r, 2000))]);
    if (this.proc.exitCode == null) this.proc.kill('SIGKILL');
    await this.exited;
  }
}

async function apiCreateBoard(base, name) {
  const res = await fetch(`${base}/api/boards`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error(`create board: ${res.status}`);
  return res.json();
}

async function apiListTodos(base) {
  const res = await fetch(`${base}/api/todos?filter=`);
  if (!res.ok) throw new Error(`list todos: ${res.status}`);
  return res.json();
}

async function apiUpdateTodo(base, id, patch) {
  const res = await fetch(`${base}/api/todos/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  if (!res.ok) throw new Error(`update todo: ${res.status}`);
  return res.json();
}

// --- page helpers ---

async function addTodo(page, text) {
  await page.fill('input[placeholder="Add a top-level todo…"]', text);
  await page.press('input[placeholder="Add a top-level todo…"]', 'Enter');
}

function row(page, title) {
  return page.locator('.item').filter({ has: page.locator('.title', { hasText: title }) }).first();
}

// The item's own checkbox: children are nested inside the parent's DOM, so a
// plain descendant lookup would match theirs too.
function checkbox(page, title) {
  return row(page, title).locator(':scope > .head > input[type=checkbox]');
}

async function editTitle(page, from, to) {
  await row(page, from).locator('.title').dblclick();
  // While editing, the row's title span is replaced by the form, so scope to
  // the (globally unique, single-edit) form instead of the row.
  const form = page.locator('form.edit');
  await form.locator('input[type=text]').first().fill(to);
  await form.locator('button[type=submit]').click();
}

async function nudgeSync(page) {
  // Focus and visibility re-attempt immediately; either unblocks a stalled
  // flush without waiting for the backoff timer.
  await page.evaluate(() => {
    window.dispatchEvent(new Event('focus'));
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

async function badgeText(page) {
  return (await page.locator('.sync').textContent().catch(() => null))?.trim() ?? null;
}

async function main() {
  const tmp = mkdtempSync(path.join(tmpdir(), 'todo-e2e-'));
  const bin = path.join(tmp, 'todo');
  await run('go', ['build', '-o', bin, '.'], { cwd: root });

  const browser = await chromium.launch();
  let server;
  try {
    // ---- shared server/browser session across scenarios ----
    const port = 20000 + Math.floor(Math.random() * 20000);
    server = new Server(bin, root, port, path.join(tmp, 'todo.db'));
    await server.start();
    await apiCreateBoard(server.base, 'Test board');

    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    await page.goto(server.base);
    // Let the service worker install, then reload so it controls the page.
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.reload();

    // ============ 1. online completion ============
    {
      await addTodo(page, 'Online todo');
      await until(() => row(page, 'Online todo').isVisible(), { what: 'online todo visible' });
      await row(page, 'Online todo').locator('input[type=checkbox]').check();
      // Default filter hides completed todos.
      await until(async () => !(await row(page, 'Online todo').isVisible()), { what: 'completed todo hidden' });
      const list = await apiListTodos(server.base);
      const done = list.find((t) => t.title === 'Online todo');
      check('online completion reaches the server', !!done?.completed);
    }

    // Show everything for the rest of the run.
    await page.goto(`${server.base}/?filter=`);

    // ============ 2. offline create + complete, reload, reconnect ============
    {
      await addTodo(page, 'Existing todo');
      await until(() => row(page, 'Existing todo').isVisible(), { what: 'existing todo visible' });

      await server.stop();
      await addTodo(page, 'Offline task');
      await until(() => row(page, 'Offline task').isVisible(), { what: 'offline task projected' });
      check('offline create projects optimistically', await row(page, 'Offline task').evaluate((el) => el.classList.contains('pending')));
      check('offline badge appears', (await badgeText(page))?.startsWith('Offline'));

      await row(page, 'Existing todo').locator('input[type=checkbox]').check();
      await until(async () => await row(page, 'Existing todo').evaluate((el) => el.classList.contains('pending')), {
        what: 'existing todo marked pending',
      });
      check('offline complete projects optimistically', await row(page, 'Existing todo').locator('input[type=checkbox]').isChecked());

      // Reload while offline: the shell and list come from the worker cache
      // and the queue from localStorage, so the projection survives.
      await page.reload();
      await until(() => row(page, 'Offline task').isVisible(), { what: 'offline task after offline reload' });
      check('queue survives an offline reload', await row(page, 'Offline task').evaluate((el) => el.classList.contains('pending')));

      await server.start();
      await nudgeSync(page);
      await until(async () => !(await row(page, 'Offline task').evaluate((el) => el.classList.contains('pending'))), {
        what: 'offline task synced',
      });
      check('reconnect syncs the queued create', true);
      const list = await apiListTodos(server.base);
      const created = list.find((t) => t.title === 'Offline task');
      const existing = list.find((t) => t.title === 'Existing todo');
      check('queued create reached the server', !!created);
      check('queued completion reached the server', !!existing?.completed);
      check('badge clears after sync', (await badgeText(page)) === null);
    }

    // ============ 2b. incoming server-side changes pull on reconnect ============
    {
      await addTodo(page, 'Remote edit');
      await until(() => row(page, 'Remote edit').isVisible(), { what: 'remote target visible' });

      await server.stop();
      // A local change queues offline (must be pushed on reconnect)…
      await editTitle(page, 'Remote edit', 'Local rename');
      await until(() => row(page, 'Local rename').isVisible(), { what: 'offline rename projected' });

      // …while "another client" — a second server on the same database —
      // completes the todo server-side. Two servers never run concurrently,
      // so the shared SQLite file is never contended.
      const other = new Server(bin, root, server.port + 1, server.dbPath);
      await other.start();
      const remote = (await apiListTodos(other.base)).find((t) => t.title === 'Remote edit');
      const done = await fetch(`${other.base}/api/todos/${remote.id}/complete`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ completed: true }),
      });
      if (!done.ok) throw new Error('other-client complete failed');
      await other.stop();

      await server.start();
      // Reconnect (the OS-level online event): push the queue, then pull the
      // server's state — the other client's completion must appear in the
      // already-open page without any manual refresh.
      await page.evaluate(() => window.dispatchEvent(new Event('online')));
      await until(async () => await row(page, 'Local rename').locator('input[type=checkbox]').isChecked(), {
        what: 'server-side completion pulled on reconnect',
        timeout: 15000,
      });
      check('incoming changes sync on reconnect without a refresh', true);
      check('queued change pushed and confirmed', await row(page, 'Local rename').evaluate((el) => !el.classList.contains('pending')));
    }

    // ============ 2c. agreeing values must not raise the merge dialog ============
    {
      // Complete a parent and its child offline in this client. The parent's
      // replay cascades completion to the child server-side, so when the
      // child's own intent is checked the server already agrees with it —
      // identical values ("done" vs "done") must NOT open the merge dialog.
      await addTodo(page, 'Cascade parent');
      await until(() => row(page, 'Cascade parent').isVisible(), { what: 'cascade parent visible' });
      await row(page, 'Cascade parent').getByRole('button', { name: 'Add child' }).click();
      await page.fill('input[placeholder="Add a subtask…"]', 'Cascade child');
      await page.press('input[placeholder="Add a subtask…"]', 'Enter');
      await until(() => row(page, 'Cascade child').isVisible(), { what: 'cascade child visible' });

      await server.stop();
      await checkbox(page, 'Cascade parent').check();
      await until(async () => await row(page, 'Cascade parent').evaluate((el) => el.classList.contains('pending')), {
        what: 'parent complete queued',
      });
      await checkbox(page, 'Cascade child').check();
      await until(async () => await row(page, 'Cascade child').evaluate((el) => el.classList.contains('pending')), {
        what: 'child complete queued',
      });

      await server.start();
      await nudgeSync(page);
      await until(async () => {
        const parent = row(page, 'Cascade parent');
        const child = row(page, 'Cascade child');
        return (
          !(await parent.evaluate((el) => el.classList.contains('pending'))) &&
          !(await child.evaluate((el) => el.classList.contains('pending')))
        );
      }, { what: 'cascade completions synced' });
      const dialog = page.locator('.modal[role=dialog]');
      check('no merge dialog when values already agree', !(await dialog.isVisible().catch(() => false)));
      const list = await apiListTodos(server.base);
      const parent = list.find((t) => t.title === 'Cascade parent');
      const child = list.find((t) => t.title === 'Cascade child');
      check('cascade completions both landed', !!parent?.completed && !!child?.completed);
      check('badge clears without a dialog', (await badgeText(page)) === null);
    }

    // ============ 3. wedged navigator.onLine ============
    {
      await server.stop();
      await addTodo(page, 'Wedged todo');
      await until(() => row(page, 'Wedged todo').isVisible(), { what: 'wedged todo projected' });

      await server.start();
      // Wedge the flag false, as Linux can after sleep/network changes: the
      // app must still replay because flushes never consult the flag.
      await page.evaluate(() => {
        Object.defineProperty(Navigator.prototype, 'onLine', { get: () => false, configurable: true });
      });
      await nudgeSync(page);
      await until(async () => !(await row(page, 'Wedged todo').evaluate((el) => el.classList.contains('pending'))), {
        what: 'wedged todo synced despite false onLine flag',
      });
      const list = await apiListTodos(server.base);
      check('flushes run regardless of navigator.onLine', !!list.find((t) => t.title === 'Wedged todo'));
    }

    // ============ 4. merge dialog ============
    {
      // -- keep my changes --
      await addTodo(page, 'Merge me');
      await until(() => row(page, 'Merge me').isVisible(), { what: 'merge target visible' });

      await server.stop();
      await editTitle(page, 'Merge me', 'Mine edited');
      await until(() => row(page, 'Mine edited').isVisible(), { what: 'offline edit projected' });

      await server.start();
      // Another device edits the same todo while ours was queued.
      const list = await apiListTodos(server.base);
      const target = list.find((t) => t.title === 'Merge me');
      await apiUpdateTodo(server.base, target.id, { title: 'Server edited' });

      await nudgeSync(page);
      const dialog = page.locator('.modal[role=dialog]');
      await until(() => dialog.isVisible(), { what: 'merge dialog appears' });
      const dialogText = await dialog.textContent();
      check('merge dialog shows both sides', dialogText.includes('Mine edited') && dialogText.includes('Server edited'));
      check('badge signals review needed', (await badgeText(page)) === 'Needs review');

      await dialog.getByRole('button', { name: 'Keep my changes' }).click();
      await until(async () => !(await dialog.isVisible().catch(() => false)), { what: 'merge dialog closed' });
      await until(() => row(page, 'Mine edited').isVisible(), { what: 'kept title visible' });
      const after = (await apiListTodos(server.base)).find((t) => t.id === target.id);
      check('keep-mine applies my edit server-side', after?.title === 'Mine edited');

      // -- keep server version, via Decide later --
      await server.stop();
      await editTitle(page, 'Mine edited', 'Second edit');
      await until(() => row(page, 'Second edit').isVisible(), { what: 'second offline edit projected' });

      await server.start();
      await apiUpdateTodo(server.base, target.id, { title: 'Server again' });

      await nudgeSync(page);
      await until(() => dialog.isVisible(), { what: 'merge dialog reappears' });
      await dialog.getByRole('button', { name: 'Decide later' }).click();
      await until(async () => !(await dialog.isVisible().catch(() => false)), { what: 'dialog deferred' });
      check('deferred conflict blocks queue', (await badgeText(page)) === 'Needs review');

      // Reopen via the badge and keep the server's version.
      await page.locator('.sync').click();
      await until(() => dialog.isVisible(), { what: 'dialog reopened from badge' });
      await dialog.getByRole('button', { name: 'Keep server version' }).click();
      await until(async () => !(await dialog.isVisible().catch(() => false)), { what: 'dialog closed again' });
      await until(async () => {
        const list = await apiListTodos(server.base);
        return list.find((t) => t.id === target.id)?.title === 'Server again';
      }, { what: 'server version kept' });
      check('keep-server drops my edit', true);
      await until(async () => (await badgeText(page)) === null, { what: 'badge clears after merge' });
    }

    // ============ 5. deleting an offline-created todo ============
    {
      await server.stop();
      await addTodo(page, 'Cancel me');
      await until(() => row(page, 'Cancel me').isVisible(), { what: 'cancel target projected' });
      page.once('dialog', (d) => d.accept());
      await row(page, 'Cancel me').getByRole('button', { name: 'Delete' }).click();
      await until(async () => !(await row(page, 'Cancel me').isVisible().catch(() => false)), { what: 'pending todo removed from view' });

      await server.start();
      await nudgeSync(page);
      // The create was cancelled, not replayed: after a moment nothing syncs.
      await new Promise((r) => setTimeout(r, 1500));
      const list = await apiListTodos(server.base);
      check('cancelled offline create never reaches the server', !list.some((t) => t.title === 'Cancel me'));
    }

    await context.close();
  } finally {
    await browser.close();
    await server?.stop();
    rmSync(tmp, { recursive: true, force: true });
  }

  if (failures) {
    console.error(`\n${failures} check(s) failed`);
    process.exit(1);
  }
  console.log('\nall checks passed');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
