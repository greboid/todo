// Thin fetch wrapper for the todo JSON API.
const base = '/api';

// Set while an auth-triggered reload is in flight so a 401 never reloads
// twice; cleared on the first successful response after the reload.
const AUTH_RELOAD_KEY = 'todo-auth-reloaded';

// Hard timeout for every request. A wedged transport (a stuck intermediary,
// a browser that silently dropped the connection) must surface as a network
// failure — mutations queue for replay, reads show an error — instead of
// leaving the UI hung on a request that will never answer. 30s is far above
// any legitimate local response.
const REQUEST_TIMEOUT_MS = 30000;

function url(path, params) {
  if (!params) return base + path;
  return `${base + path}?${new URLSearchParams(params)}`;
}

async function req(path, { body, method, params, meta } = {}) {
  const res = await fetch(url(path, params), {
    method,
    signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    ...(body !== undefined && {
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  });
  if (res.status === 204) return null;
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    // A 401 means the browser session lapsed (idle past its expiry or a
    // server restart): reload once to pick up the fresh cookie the server
    // mints with the page. The flag stops a loop when reloading doesn't help.
    if (res.status === 401 && !sessionStorage.getItem(AUTH_RELOAD_KEY)) {
      sessionStorage.setItem(AUTH_RELOAD_KEY, '1');
      window.location.reload();
      return new Promise(() => {});
    }
    const err = new Error(json.error || `request failed: ${res.status}`);
    err.status = res.status;
    throw err;
  }
  // Marker for the store's SSE echo suppression: a poke arriving right
  // after this tab's own mutation is its own echo, not someone else's
  // change. Schedule parse/extract are POSTs but never mutate.
  if (method && method !== 'GET' && !path.startsWith('/schedule/')) {
    api.lastMutationAt = Date.now();
  }
  // The service worker serves cached todos lists stamped with the local date
  // they were computed for (X-Todo-Cache-Day). Callers that need to detect
  // an outdated offline view pass a `meta` object to receive it (null when
  // the response is fresh — the header only exists on cache serves).
  if (meta) meta.cacheDay = res.headers.get('x-todo-cache-day') || null;
  sessionStorage.removeItem(AUTH_RELOAD_KEY);
  return json;
}

// Timestamp of this client's last successful mutation, used by the store to
// skip the SSE echo of its own changes. 0 until the first mutation.
export const api = {
  lastMutationAt: 0,
  listTodos: (boardId, filter, today, meta) => {
    const params = {};
    if (boardId) params.boardId = boardId;
    if (filter) params.filter = filter;
    if (today) params.today = today;
    return req('/todos', { params: Object.keys(params).length ? params : undefined, meta });
  },
  createTodo: (data) => req('/todos', { method: 'POST', body: data }),
  getTodo: (id) => req(`/todos/${id}`),
  updateTodo: (id, data) => req(`/todos/${id}`, { method: 'PATCH', body: data }),
  deleteTodo: (id) => req(`/todos/${id}`, { method: 'DELETE' }),
  moveTodo: (id, data) => req(`/todos/${id}/move`, { method: 'POST', body: data }),
  completeTodo: (id, completed) => req(`/todos/${id}/complete`, { method: 'POST', body: { completed } }),
  parseSchedule: (text) => {
    // Send the browser's local today so relative dates ("tomorrow") resolve
    // from the user's perspective, matching the old client-side parser.
    const d = new Date();
    const today = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    return req('/schedule/parse', { method: 'POST', body: { text, today } });
  },
  extractSchedule: (text) => {
    const d = new Date();
    const today = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    return req('/schedule/extract', { method: 'POST', body: { text, today } });
  },
  listLabels: () => req('/labels'),
  updateLabel: (name, color) => req(`/labels/${encodeURIComponent(name)}`, { method: 'PUT', body: { color } }),
  deleteLabel: (name) => req(`/labels/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  addPredefinedLabel: (name) => req('/labels/predefined', { method: 'POST', body: { name } }),
  listPriorities: () => req('/priorities'),
  updatePriority: (name, color) => req(`/priorities/${encodeURIComponent(name)}`, { method: 'PUT', body: { color } }),
  addPredefinedPriority: (name) => req('/priorities/predefined', { method: 'POST', body: { name } }),
  reorderPriorities: (names) => req('/priorities/reorder', { method: 'POST', body: { names } }),
  removePredefinedPriority: (name) => req(`/priorities/predefined/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  listBoards: () => req('/boards'),
  createBoard: (data) => req('/boards', { method: 'POST', body: data }),
  getBoard: (id) => req(`/boards/${id}`),
  updateBoard: (id, data) => req(`/boards/${id}`, { method: 'PATCH', body: data }),
  deleteBoard: (id) => req(`/boards/${id}`, { method: 'DELETE' }),
  listSavedSearches: () => req('/saved-searches'),
  createSavedSearch: (data) => req('/saved-searches', { method: 'POST', body: data }),
  deleteSavedSearch: (id) => req(`/saved-searches/${id}`, { method: 'DELETE' }),
};
