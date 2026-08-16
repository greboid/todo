import path from 'node:path';
import { createHash } from 'node:crypto';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Build output is written directly into the Go embed directory so `go build`
// picks it up with no copy step.
export default defineConfig({
  plugins: [svelte(), precacheServiceWorker()],
  publicDir: 'static',
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
});

// Rewrites the service worker (copied from web/static into the output) with
// the final precache list and a content-derived version, so the worker
// re-installs exactly when the app shell it precaches changes.
function precacheServiceWorker() {
  return {
    name: 'precache-service-worker',
    closeBundle: {
      sequential: true,
      async handler() {
        const outDir = path.resolve(import.meta.dirname, '../internal/ui/dist');
        const urls = ['/'];
        await collect(outDir, '', urls);
        urls.sort();
        const file = path.join(outDir, 'sw.js');
        let src = await readFile(file, 'utf8');
        const version = createHash('sha256').update(src).update(JSON.stringify(urls)).digest('hex').slice(0, 16);
        src = src
          .replace('const VERSION = \'__VERSION__\';', `const VERSION = ${JSON.stringify(version)};`)
          .replace('const PRECACHE_URLS = \'__PRECACHE_URLS__\';', `const PRECACHE_URLS = ${JSON.stringify(urls)};`);
        await writeFile(file, src);
      },
    },
  };
}

async function collect(dir, prefix, urls) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) {
      await collect(path.join(dir, entry.name), rel, urls);
    } else if (entry.name !== 'sw.js') {
      urls.push(`/${rel}`);
    }
  }
}
