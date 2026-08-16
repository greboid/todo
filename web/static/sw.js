// Service worker for the Todo app.
//
// Responsibilities:
//   - Precache the app shell (index.html + hashed assets + icons + manifest)
//     at install so the UI loads with no connection.
//   - Network-first proxy for /api GETs: successful responses are cached and
//     served back when offline, so lists render from the last good sync.
//   - Cache POST /api/schedule/{parse,extract} responses keyed by request
//     body, so the server-side date grammar keeps working offline for inputs
//     seen before.
//   - Report the outcome of its network-first fetches to every open tab
//     (postMessage), so the UI's offline indicator keys off observed
//     reachability — navigator.onLine stays true under DevTools throttling
//     and other "connected but no route" states.
//
// Mutations are deliberately NOT handled here. The store queues them while
// offline (see src/lib/offline.svelte.js) and replays them against the API
// when connectivity returns, so the offline story lives in one place.
//
// The `VERSION` and `PRECACHE_URLS` literals are placeholders: the Vite build
// (vite.config.js, precacheServiceWorker plugin) rewrites this file in the
// output directory with the final asset list and a content-derived version.
const VERSION = '__VERSION__';
const PRECACHE_URLS = '__PRECACHE_URLS__';

const PRECACHE = `todo-precache-${VERSION}`;
const DATA = `todo-data-${VERSION}`;
const SCHEDULE = `todo-schedule-${VERSION}`;

const SCHEDULE_PATHS = new Set(['/api/schedule/parse', '/api/schedule/extract']);
const SCHEDULE_CACHE_MAX = 300;

// Tell every open tab whether the network was actually reachable. Sent on
// each outcome (not just transitions) so a tab that loads straight into
// offline learns the state from its first API response.

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(PRECACHE)
      // 'reload' bypasses any stale HTTP-cache entries from a previous build.
      .then((cache) => cache.addAll(PRECACHE_URLS.map((u) => new Request(u, { cache: 'reload' }))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keep = new Set([PRECACHE, DATA, SCHEDULE]);
      for (const name of await caches.keys()) {
        if (!keep.has(name)) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (new URL(req.url).origin !== self.location.origin) return;

  if (req.method === 'POST' && SCHEDULE_PATHS.has(new URL(req.url).pathname)) {
    event.respondWith(scheduleParse(req, new URL(req.url).pathname));
    return;
  }
  if (req.method !== 'GET') return; // mutations: the app queues them itself

  if (req.mode === 'navigate') {
    event.respondWith(navigate(req));
    return;
  }
  const pathname = new URL(req.url).pathname;
  if (pathname.startsWith('/api/')) {
    if (pathname.startsWith('/api/swagger')) return; // API docs: online only
    event.respondWith(apiGet(req));
    return;
  }
  event.respondWith(asset(req));
});

// SPA navigations: fresh document while online (and refresh the cache), the
// cached shell when offline.
async function navigate(req) {
  try {
    const res = await fetch(req, { cache: 'no-cache' });
    reportNetwork(true);
    const cache = await caches.open(PRECACHE);
    await cache.put('/', res.clone());
    return res;
  } catch {
    reportNetwork(false);
    return (await caches.match('/')) || offlinePage();
  }
}

// API reads: network-first, fall back to the last successful response.
async function apiGet(req) {
  try {
    const res = await fetch(req);
    reportNetwork(true);
    if (res.ok) {
      const cache = await caches.open(DATA);
      await cache.put(req, res.clone());
    }
    return res;
  } catch {
    reportNetwork(false);
    return (await caches.match(req)) || Response.error();
  }
}

function reportNetwork(up) {
  self.clients
    .matchAll({ type: 'window' })
    .then((clients) => {
      for (const client of clients) client.postMessage({ type: 'todo-network', up });
    })
    .catch(() => {});
}

// Schedule parsing: cache-first keyed by endpoint + request body. Dates typed
// before parse identically offline; new inputs fail and the UI falls back.
async function scheduleParse(req, pathname) {
  const body = await req.clone().text();
  const key = scheduleKey(pathname, body);
  const cached = await caches.match(key);
  if (cached) return cached;
  const res = await fetch(req);
  if (res.ok) {
    const cache = await caches.open(SCHEDULE);
    await cache.put(key, res.clone());
    const keys = await cache.keys();
    if (keys.length > SCHEDULE_CACHE_MAX) await cache.delete(keys[0]);
  }
  return res;
}

function scheduleKey(pathname, body) {
  const encoded = encodeURIComponent(body.slice(0, 500));
  return new Request(`${self.location.origin}/__sw/schedule${pathname}?b=${encoded}`);
}

// Same-origin static assets: cache-first (names are content-hashed by Vite),
// network fill for anything the precache missed.
async function asset(req) {
  const cached = await caches.match(req);
  if (cached) return cached;
  try {
    const res = await fetch(req);
    if (res.ok && res.type === 'basic') {
      const cache = await caches.open(PRECACHE);
      await cache.put(req, res.clone());
    }
    return res;
  } catch {
    return cached || Response.error();
  }
}

function offlinePage() {
  return new Response('<!doctype html><title>Offline</title><p>Offline and the app has not been cached yet.</p>', {
    status: 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  });
}
