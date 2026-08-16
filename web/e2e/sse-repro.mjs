// Repro: Firefox, two tabs, service worker active. Watch every /api request
// for failures (NS_BINDING_ABORTED etc.) over ~2 minutes including foreign
// mutations, reconnects, and the 30s client health sweep.
import { execFile, spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { firefox } from 'playwright';

const root = path.resolve(import.meta.dirname, '../..');
const run = promisify(execFile);
const port = 21000 + Math.floor(Math.random() * 2000);
const base = `http://127.0.0.1:${port}`;

async function until(fn, { timeout = 15000, interval = 100, what = 'condition' } = {}) {
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
  constructor(bin, dbPath) {
    this.bin = bin;
    this.dbPath = dbPath;
  }
  async start() {
    this.proc = spawn(this.bin, [], {
      cwd: root,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, TODO_ADDR: `127.0.0.1:${port}`, TODO_DB: this.dbPath, TODO_DB_DRIVER: 'sqlite' },
    });
    this.log = [];
    this.proc.stderr.on('data', (d) => this.log.push(d.toString()));
    this.proc.stdout.on('data', (d) => this.log.push(d.toString()));
    this.exited = once(this.proc, 'exit').then(() => {});
    await until(async () => (await fetch(`${base}/api/boards`).catch(() => null))?.ok, { what: 'server ready' });
  }
  async stop() {
    if (!this.proc || this.proc.exitCode != null) return;
    this.proc.kill('SIGTERM');
    await Promise.race([this.exited, new Promise((r) => setTimeout(r, 2000))]);
    if (this.proc.exitCode == null) this.proc.kill('SIGKILL');
    await this.exited;
  }
}

function wire(name, page) {
  const state = { armed: false };
  wire.state = state;
  page.on('requestfailed', (req) => {
    const u = new URL(req.url());
    if (!u.pathname.startsWith('/api')) return;
    if (!state.armed) {
      console.log(`[${name}] (setup, ignored) ${req.method()} ${u.pathname} :: ${req.failure()?.errorText}`);
      return;
    }
    console.log(`[${name}] FAILED ${req.method()} ${u.pathname} :: ${req.failure()?.errorText}`);
  });
  page.on('response', (res) => {
    const u = new URL(res.url());
    if (!u.pathname.startsWith('/api') || u.pathname === '/api/events') return;
    if (res.status() >= 400) console.log(`[${name}] HTTP ${res.status()} ${u.pathname}`);
  });
  page.on('console', (m) => {
    if (m.type() === 'error') console.log(`[${name}] console: ${m.text()}`);
  });
  return state;
}

const tmp = mkdtempSync(path.join(tmpdir(), 'todo-repro-'));
const bin = path.join(tmp, 'todo');
await run('go', ['build', '-o', bin, '.'], { cwd: root });
const server = new Server(bin, path.join(tmp, 'todo.db'));
await server.start();
await fetch(`${base}/api/boards`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: 'repro' }) });

const apiAdd = async (title) => {
  const res = await fetch(`${base}/api/todos`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ title, boardId: 1 }),
  });
  if (!res.ok) throw new Error(`api add: ${res.status}`);
};

const browser = await firefox.launch();
try {
  const context = await browser.newContext();
  const page1 = await context.newPage();
  const t1 = wire('tab1', page1);
  await page1.goto(base);
  await page1.evaluate(() => navigator.serviceWorker.ready);
  await page1.reload();
  t1.armed = true;
  console.log('--- tab1 up, SW controlling');

  // Foreign mutation: tab1 must show it with zero interaction.
  await apiAdd('tab1 poke');
  await until(async () => await page1.locator('.item').filter({ hasText: 'tab1 poke' }).isVisible().catch(() => false), { what: 'tab1 sees poke' });
  console.log('--- tab1 SSE poke OK');

  const page2 = await context.newPage();
  const t2 = wire('tab2', page2);
  await page2.goto(base);
  await page2.evaluate(() => navigator.serviceWorker.ready);
  await page2.reload();
  t2.armed = true;
  console.log('--- tab2 up');

  await apiAdd('tab2 poke');
  await until(async () => await page2.locator('.item').filter({ hasText: 'tab2 poke' }).isVisible().catch(() => false), { what: 'tab2 sees poke' });
  console.log('--- tab2 SSE poke OK');
  await until(async () => await page1.locator('.item').filter({ hasText: 'tab2 poke' }).isVisible().catch(() => false), { what: 'tab1 sees tab2 poke' });
  console.log('--- tab1 sees tab2 poke OK');

  // Idle long enough for heartbeats (20s) and the client sweep (30s).
  console.log('--- idling 75s (heartbeats + sweeps)...');
  await new Promise((r) => setTimeout(r, 75000));

  await apiAdd('post-idle poke');
  const ok1 = await until(async () => await page1.locator('.item').filter({ hasText: 'post-idle poke' }).isVisible().catch(() => false), { timeout: 20000, what: 'tab1 post-idle poke' }).then(() => true, (e) => (console.log(e.message), false));
  const ok2 = await until(async () => await page2.locator('.item').filter({ hasText: 'post-idle poke' }).isVisible().catch(() => false), { timeout: 20000, what: 'tab2 post-idle poke' }).then(() => true, (e) => (console.log(e.message), false));
  console.log(`--- post-idle: tab1=${ok1} tab2=${ok2}`);

  // Sanity: labels/priorities/saved-searches requests succeed in both tabs.
  for (const [name, page] of [['tab1', page1], ['tab2', page2]]) {
    const out = await page.evaluate(async () => {
      const r = {};
      for (const p of ['labels', 'priorities', 'saved-searches']) {
        try {
          const res = await fetch(`/api/${p}`);
          r[p] = res.status;
        } catch (e) {
          r[p] = String(e);
        }
      }
      return r;
    });
    console.log(`--- ${name} api probes: ${JSON.stringify(out)}`);
  }
  console.log(server.log.join('').trim() ? `--- server log:\n${server.log.join('')}` : '--- server log empty');
} finally {
  await browser.close();
  await server.stop();
  rmSync(tmp, { recursive: true, force: true });
}
