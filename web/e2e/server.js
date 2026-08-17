// E2E wrapper around the single-binary Go server: spawn on a unique port
// against a throwaway SQLite database, wait for readiness, stop cleanly.
// Tests stop/start the instance at will (offline simulation, key rotation);
// the binary path comes from E2E_BIN (built once by global-setup). A Server
// constructed without an explicit dbPath owns its temp directory and removes
// it on stop; one constructed with a shared dbPath (the "other client" in
// reconnect tests) never deletes it.
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { boards, createBoard } from './helpers.js';

const root = path.resolve(import.meta.dirname, '../..');

export class Server {
  constructor({ port, dbPath = null, apiKey = '' }) {
    this.port = port;
    this.apiKey = apiKey;
    this.ownsDb = dbPath == null;
    if (this.ownsDb) {
      this.tmp = mkdtempSync(path.join(tmpdir(), 'todo-e2e-'));
      dbPath = path.join(this.tmp, 'todo.db');
    }
    this.dbPath = dbPath;
  }

  get base() {
    return `http://127.0.0.1:${this.port}`;
  }

  async start() {
    this.proc = spawn(process.env.E2E_BIN, this.apiKey ? ['-api-key', this.apiKey] : [], {
      cwd: root,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, TODO_ADDR: `127.0.0.1:${this.port}`, TODO_DB: this.dbPath, TODO_DB_DRIVER: 'sqlite' },
    });
    this.exited = once(this.proc, 'exit').then(() => {});
    const deadline = Date.now() + 15000;
    while (Date.now() < deadline) {
      const res = await fetch(`${this.base}/api/boards`, { headers: this.apiKey ? { 'X-API-Key': this.apiKey } : {} }).catch(() => null);
      if (res && res.ok) return;
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error(`server on :${this.port} did not become ready`);
  }

  async stop() {
    if (!this.proc || this.proc.exitCode != null) return;
    this.proc.kill('SIGTERM');
    await Promise.race([this.exited, new Promise((r) => setTimeout(r, 2000))]);
    if (this.proc.exitCode == null) this.proc.kill('SIGKILL');
    await this.exited;
  }

  // The app requires at least one board; make sure it exists exactly once
  // per server instance (idempotent, memoised).
  async ensureBoard(name = 'Test board') {
    if (this.boardId != null) return this.boardId;
    const existing = await boards(this.base, this.apiKey);
    this.boardId = existing.length ? existing[0].id : (await createBoard(this.base, name, this.apiKey)).id;
    return this.boardId;
  }

  async cleanup() {
    if (this.ownsDb && this.tmp) rmSync(this.tmp, { recursive: true, force: true });
  }
}
