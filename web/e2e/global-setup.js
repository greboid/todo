// Builds the single Go binary once per run into a temp dir; every worker's
// server fixture (e2e/fixtures.js) spawns that binary. The path is shared
// via E2E_BIN, which worker processes inherit from the runner.
import { execFile } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';

const run = promisify(execFile);
const root = path.resolve(import.meta.dirname, '../..');

export default async function globalSetup() {
  const tmp = mkdtempSync(path.join(tmpdir(), 'todo-e2e-bin-'));
  const bin = path.join(tmp, 'todo');
  await run('go', ['build', '-o', bin, '.'], { cwd: root });
  process.env.E2E_BIN = bin;
  return async () => {
    rmSync(tmp, { recursive: true, force: true });
  };
}
