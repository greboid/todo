// Thin fetch wrapper for the todo JSON API.
const base = '/api';

function url(path, params) {
  if (!params) return base + path;
  return `${base + path}?${new URLSearchParams(params)}`;
}

async function req(path, { body, method, params } = {}) {
  const res = await fetch(url(path, params), {
    method,
    ...(body !== undefined && {
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  });
  if (res.status === 204) return null;
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(json.error || `request failed: ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return json;
}

export const api = {
  listTodos: (boardId, filter, today) => {
    const params = {};
    if (boardId) params.boardId = boardId;
    if (filter) params.filter = filter;
    if (today) params.today = today;
    return req('/todos', { params: Object.keys(params).length ? params : undefined });
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
  addPredefinedLabel: (name) => req('/labels/predefined', { method: 'POST', body: { name } }),
  removePredefinedLabel: (name) => req(`/labels/predefined/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  listPriorities: () => req('/priorities'),
  updatePriority: (name, color) => req(`/priorities/${encodeURIComponent(name)}`, { method: 'PUT', body: { color } }),
  addPredefinedPriority: (name) => req('/priorities/predefined', { method: 'POST', body: { name } }),
  removePredefinedPriority: (name) => req(`/priorities/predefined/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  listBoards: () => req('/boards'),
  createBoard: (data) => req('/boards', { method: 'POST', body: data }),
  getBoard: (id) => req(`/boards/${id}`),
  updateBoard: (id, data) => req(`/boards/${id}`, { method: 'PATCH', body: data }),
  deleteBoard: (id) => req(`/boards/${id}`, { method: 'DELETE' }),
};
