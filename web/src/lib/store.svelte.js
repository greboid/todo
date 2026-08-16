// Central reactive store. Lives in a .svelte.js module so Svelte 5 runes
// ($state, $derived) work both here and in components that import it.
import { api } from './api.js';
import { offline } from './offline.svelte.js';

// Safe localStorage helpers: access can throw in private mode or sandboxed
// iframes, so wrap every read/write and degrade to no-op on failure.
const storage = {
  get: (key) => {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set: (key, value) => {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* ignore */
    }
  },
};

// Shared ordering: by position, falling back to id for stable ties.
const byPositionThenId = (a, b) => a.position - b.position || a.id - b.id;

// Palette of colours used when a label has no user-defined colour. Picked
// deterministically by hashing the label name so the same label always gets
// the same colour across reloads and clients.
const LABEL_PALETTE = [
  '#ef4444', // red
  '#f97316', // orange
  '#f59e0b', // amber
  '#84cc16', // lime
  '#22c55e', // green
  '#14b8a6', // teal
  '#06b6d4', // cyan
  '#3b82f6', // blue
  '#6366f1', // indigo
  '#8b5cf6', // violet
  '#ec4899', // pink
  '#64748b', // slate
];

function labelColor(name, color) {
  if (color) return color;
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return LABEL_PALETTE[Math.abs(hash) % LABEL_PALETTE.length];
}

export const store = createStore();
export { labelColor, LABEL_PALETTE };

function createStore() {
  // todos holds the server's response for the active board + filter, with
  // any queued offline changes projected on top (see offline.svelte.js).
  // serverTodos keeps the raw response so the projection can be recomputed
  // after every enqueue. The store does no client-side filtering — the
  // grammar lives in internal/filter.
  let serverTodos = $state([]);
  let todos = $state([]);
  let loading = $state(false);
  let error = $state(null);
  let labels = $state([]);
  let priorities = $state([]);
  let boards = $state([]);
  let savedSearches = $state([]);
  // Active board is the one currently shown. Persisted across reloads; falls
  // back to the first board if the stored id no longer exists. May also be set
  // from the URL (?board=<id>) which takes precedence over the stored value.
  let activeBoardId = $state(Number(storage.get('todo:activeBoard')) || null);

  // Single-edit enforcement: only one todo may be edited at a time.
  let editingId = $state(null);
  let editingDirty = $state(false);
  let rejectionTick = $state(0);

  // Single right-click context menu: holds the id of the todo whose action
  // buttons the menu duplicates, plus the cursor position in viewport
  // coordinates (the menu is rendered position:fixed by the owning item).
  let contextMenu = $state(null);

  // --- Filter ---
  // The list filter is evaluated entirely server-side (GET /api/todos?filter=).
  // Syntax: label:<name>, date:<preset|YYYY-MM-DD|range>, has:<complete|label|
  // recur|date>, and bare search text. Prepend ! to label/date/has for negation.
  // Defaults to "!has:complete" (hide completed). Persisted. May also be set
  // from the URL (?filter=<text>) which takes precedence over the stored value.
  // An invalid token makes the API return 400, surfaced here as filterError.
  let filterText = $state(storage.get('todo:filter') || '!has:complete');
  let filterError = $state('');
  let filterTimer = null;

  function persistFilter() {
    storage.set('todo:filter', filterText);
  }

  // Apply new filter text: persist it, mirror it into the URL, and re-fetch
  // (debounced) so the server re-evaluates the list.
  function applyFilterText(text) {
    filterText = text;
    persistFilter();
    syncURL();
    clearTimeout(filterTimer);
    filterTimer = setTimeout(() => {
      load();
    }, 200);
  }

  // --- URL sync ---
  // board and filter are mirrored into the query string so views are
  // shareable/bookmarkable and navigable via back/forward.
  function readURLParams() {
    try {
      const params = new URLSearchParams(window.location.search);
      const board = params.get('board');
      const filter = params.get('filter');
      if (board != null && board !== '') {
        const parsed = Number(board);
        if (Number.isFinite(parsed) && parsed > 0) activeBoardId = parsed;
      }
      if (filter != null) filterText = filter;
    } catch {
      /* window/location unavailable — ignore */
    }
  }

  let urlTimer = null;
  function syncURL() {
    clearTimeout(urlTimer);
    urlTimer = setTimeout(doSyncURL, 1000);
  }

  function doSyncURL() {
    try {
      const params = new URLSearchParams(window.location.search);
      let changed = false;
      const boardVal = activeBoardId != null ? String(activeBoardId) : '';
      if (params.get('board') !== boardVal) {
        if (boardVal) params.set('board', boardVal);
        else params.delete('board');
        changed = true;
      }
      if (!filterError && params.get('filter') !== filterText) {
        if (filterText) params.set('filter', filterText);
        else params.delete('filter');
        changed = true;
      }
      if (changed) {
        const qs = params.toString();
        const url = qs ? `?${qs}` : window.location.pathname;
        window.history.pushState(null, '', url);
      }
    } catch {
      /* window/history unavailable — ignore */
    }
  }

  // ISO date (YYYY-MM-DD) in the user's local timezone. Sent to the API so date
  // presets (today/tomorrow/week) resolve from the user's perspective.
  function todayISO() {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }

  function byId(id) {
    return todos.find((t) => t.id === id);
  }

  function childrenOf(parentId) {
    // parentId === null means top-level roots.
    return todos
      .filter((t) => (parentId === null ? t.parentId == null : t.parentId === parentId))
      .sort(byPositionThenId);
  }

  // Display view of children. The server already applied the filter (matching
  // todos plus ancestor context), so this is just the tree built from todos.
  function visibleChildrenOf(parentId) {
    return childrenOf(parentId);
  }

  // Whether any filter criterion is active (beyond "show everything").
  function filterIsActive() {
    return filterText.trim() !== '';
  }

  // Compute the absolute server sibling index for dropping draggedId relative
  // to anchor (a visible todo) in the given zone ("before" | "after"). Uses the
  // stored position field (the server's gapless index) so drops stay correct
  // even when the filter hides some siblings.
  function dropPosition(draggedId, anchor, zone) {
    const dragged = byId(draggedId);
    const sameParent = dragged && (dragged.parentId ?? null) === (anchor.parentId ?? null);
    let pos = anchor.position + (zone === 'after' ? 1 : 0);
    if (sameParent && dragged && dragged.position < anchor.position) pos -= 1;
    return pos;
  }

  // Boards are returned by the API in position order; the switcher relies on it.
  function boardById(id) {
    return boards.find((b) => b.id === id);
  }

  // Attempt to start editing `id`. Returns true if the caller may proceed.
  // If another todo is already being edited:
  //   - if that edit is pristine (no changes), it is silently dropped and the
  //     new todo takes over;
  //   - if it has unsaved changes, the attempt is refused and rejectionTick is
  //     bumped so the editing todo can animate (shake) to signal the refusal.
  function beginEdit(id) {
    if (editingId != null && editingId !== id) {
      if (editingDirty) {
        rejectionTick++;
        return false;
      }
      editingId = null;
      editingDirty = false;
    }
    editingId = id;
    editingDirty = false;
    return true;
  }

  function endEdit() {
    editingId = null;
    editingDirty = false;
    // A sync deferred while the editor was open lands now (debounced). The
    // save path re-fetched already, so an extra load is only redundant.
    if (deferredSync) {
      deferredSync = false;
      scheduleSyncLoad();
    }
  }

  function markEditDirty() {
    if (editingId != null) editingDirty = true;
  }

  function openContextMenu(todoId, x, y) {
    contextMenu = { todoId, x, y };
  }

  function closeContextMenu() {
    contextMenu = null;
  }

  function reproject() {
    todos = offline.project(serverTodos);
  }

  // (Re)fetch boards, the filtered todo list for the active board, and labels.
  // A 400 means the filter is invalid: surface it as filterError and keep the
  // last good list rather than clearing it. Reconnect pulls can race
  // user-triggered loads, so a response only lands if no newer load has
  // started since (loadSeq discards the stale one).
  let loadSeq = 0;
  async function load() {
    const seq = ++loadSeq;
    loading = true;
    error = null;
    try {
      const boardList = (await api.listBoards()) ?? [];
      if (seq !== loadSeq) return;
      boards = boardList;
      if (!activeBoardId || !boardList.some((b) => b.id === activeBoardId)) {
        activeBoardId = boardList[0]?.id ?? null;
        if (activeBoardId) storage.set('todo:activeBoard', String(activeBoardId));
      }
      const [todoList, labelList, priorityList, searchList] = await Promise.all([
        api.listTodos(activeBoardId ?? undefined, filterText, todayISO()),
        api.listLabels(),
        api.listPriorities(),
        api.listSavedSearches(),
      ]);
      if (seq !== loadSeq) return;
      serverTodos = todoList ?? [];
      labels = labelList ?? [];
      priorities = priorityList ?? [];
      savedSearches = searchList ?? [];
      filterError = '';
      reproject();
      syncURL();
    } catch (e) {
      if (seq !== loadSeq) return;
      if (e.status === 400) {
        filterError = e.message;
      } else {
        error = e.message;
      }
    } finally {
      if (seq === loadSeq) loading = false;
    }
  }

  async function selectBoard(id) {
    if (id === activeBoardId) return;
    activeBoardId = id;
    storage.set('todo:activeBoard', String(id));
    syncURL();
    await load();
  }

  async function createBoard({ name }) {
    const created = await api.createBoard({ name });
    boards = [...boards, created].sort(byPositionThenId);
    // Creating the first board makes it active so the todo list appears
    // without a reload.
    if (activeBoardId == null) {
      activeBoardId = created.id;
      storage.set('todo:activeBoard', String(created.id));
      syncURL();
    }
    return created;
  }

  async function renameBoard(id, name) {
    const updated = await api.updateBoard(id, { name });
    applyBoardUpdate(updated);
    return updated;
  }

  async function reorderBoard(id, position) {
    const updated = await api.updateBoard(id, { position });
    applyBoardUpdate(updated);
    boards = (await api.listBoards()) ?? [];
    return updated;
  }

  async function deleteBoard(id) {
    await api.deleteBoard(id);
    boards = boards.filter((b) => b.id !== id);
    if (activeBoardId === id) {
      activeBoardId = boards[0]?.id ?? null;
      if (activeBoardId) storage.set('todo:activeBoard', String(activeBoardId));
      syncURL();
      await load();
    }
  }

  function applyBoardUpdate(updated) {
    const idx = boards.findIndex((b) => b.id === updated.id);
    if (idx >= 0) {
      const next = [...boards];
      next[idx] = updated;
      boards = next.sort(byPositionThenId);
    }
  }

  async function create({ title, description = '', parentId = null, labels = [], priority = '', dueDate = null, recurrence = null, rawText = null }) {
    const payload = {
      title,
      description,
      labels,
    };
    if (priority) payload.priority = priority;
    if (parentId == null) {
      payload.boardId = activeBoardId ?? undefined;
    } else {
      payload.parentId = parentId;
    }
    if (dueDate) payload.dueDate = dueDate;
    if (recurrence) payload.recurrence = recurrence;
    let created;
    try {
      created = await api.createTodo(payload);
    } catch (e) {
      // Network failure: queue the intent (with the raw quick-add text so the
      // server re-extracts it at replay) and show it optimistically.
      if (e.status === undefined) {
        offline.enqueueCreate({ boardId: activeBoardId, parentId, payload, text: rawText });
        return null;
      }
      throw e;
    }
    // The new todo may or may not match the active filter; re-fetch to reflect
    // the server's filtered view.
    await load();
    return created;
  }

  async function update(id, patch) {
    const cur = byId(id);
    if (!cur) return;
    // Todos that only exist offline (negative temp ids) have no server row
    // to patch: fold the change into the queued intents instead.
    if (offline.isTempId(id)) {
      offline.enqueueUpdate(cur, patch);
      return;
    }
    try {
      await api.updateTodo(id, patch);
    } catch (e) {
      if (e.status === undefined) {
        offline.enqueueUpdate(cur, patch);
        return;
      }
      error = e.message;
      await load();
      return;
    }
    if (patch.labels) {
      await loadLabels();
    }
    if (patch.priority !== undefined) {
      await loadPriorities();
    }
    // A field change (labels, priority, due date, title) can alter filter membership.
    await load();
  }

  // Toggle completion via the /complete endpoint, then re-fetch the filtered
  // view. Under the default "!has:complete" filter the completed todo leaves
  // the list; a completed recurring todo's spawned next instance appears.
  async function setCompleted(id, completed) {
    const cur = byId(id);
    if (!cur || cur.completed === completed) return;
    if (offline.isTempId(id)) {
      offline.enqueueComplete(cur, completed);
      return;
    }
    try {
      await api.completeTodo(id, completed);
    } catch (e) {
      if (e.status === undefined) {
        offline.enqueueComplete(cur, completed);
        return;
      }
      error = e.message;
      await load();
      return;
    }
    await load();
  }

  async function remove(id) {
    const cur = byId(id);
    if (!cur) return;
    // Deleting a pending offline create cancels its queued intents rather
    // than replaying a create+delete pair.
    if (offline.isTempId(id)) {
      offline.enqueueDelete(cur);
      return;
    }
    try {
      await api.deleteTodo(id);
    } catch (e) {
      if (e.status === undefined) {
        offline.enqueueDelete(cur);
        return;
      }
      error = e.message;
      return;
    }
    await loadLabels();
    await load();
  }

  async function move(id, { parentId, position }) {
    const cur = byId(id);
    if (!cur) return;
    const targetParent = parentId === undefined ? cur.parentId ?? null : parentId;
    if (offline.isTempId(id)) {
      offline.enqueueMove(cur, { parentId: targetParent, position });
      return;
    }
    const body = { parentId: targetParent };
    if (position != null) body.position = position;
    try {
      await api.moveTodo(id, body);
    } catch (e) {
      if (e.status === undefined) {
        offline.enqueueMove(cur, { parentId: targetParent, position });
        return;
      }
      error = e.message;
      await load();
      return;
    }
    await load();
  }

  async function loadLabels() {
    labels = (await api.listLabels()) ?? [];
  }

  async function addPredefinedLabel(name) {
    const trimmed = (name ?? '').trim();
    if (!trimmed) return;
    await api.addPredefinedLabel(trimmed);
    await loadLabels();
  }

  // Deleting a label detaches it from every todo, so filter membership can
  // change (e.g. label: filters); load() re-fetches todos and labels.
  async function deleteLabel(name) {
    await api.deleteLabel(name);
    await load();
  }

  async function updateLabelColor(name, color) {
    await api.updateLabel(name, color);
    await loadLabels();
    await load();
  }

  async function loadPriorities() {
    priorities = (await api.listPriorities()) ?? [];
  }

  async function addPredefinedPriority(name) {
    const trimmed = (name ?? '').trim();
    if (!trimmed) return;
    await api.addPredefinedPriority(trimmed);
    await loadPriorities();
  }

  async function removePredefinedPriority(name) {
    await api.removePredefinedPriority(name);
    await loadPriorities();
  }

  async function updatePriorityColor(name, color) {
    await api.updatePriority(name, color);
    await loadPriorities();
    await load();
  }

  async function reorderPriorities(names) {
    const updated = (await api.reorderPriorities(names)) ?? [];
    priorities = updated;
  }

  // --- Saved searches ---
  // Named filter queries kept server-side; applying one just sets the filter
  // text (the server re-evaluates it on the next fetch).
  async function createSavedSearch(name, query) {
    const created = await api.createSavedSearch({ name, query });
    savedSearches = [...savedSearches, created];
    return created;
  }

  async function deleteSavedSearch(id) {
    await api.deleteSavedSearch(id);
    savedSearches = savedSearches.filter((s) => s.id !== id);
  }

  function applySavedSearch(id) {
    const search = savedSearches.find((s) => s.id === id);
    if (!search) return;
    applyFilterText(search.query);
  }

  // --- Live sync (SSE) ---
  // The server streams pokes (GET /api/events) after every successful
  // mutation by any client — another tab, a phone, a curl against the API —
  // so foreign changes appear without a manual refresh. Pokes carry no
  // data: they just re-run load() (debounced, so bursts coalesce into one
  // fetch). The echo of this tab's own mutations is skipped (their handlers
  // already re-fetch), and a poke arriving while a todo is being edited is
  // deferred until the edit closes so the editor never changes underneath
  // the user.
  let eventSource = null;
  let syncTimer = null;
  let deferredSync = false;

  // A stream is healthy only while it is OPEN and bytes still arrive: the
  // server heartbeats every 20s (a named `ping` event), so nothing for
  // 60s means the connection is dead even though it still looks open
  // (sleep/wake, a proxy that silently dropped it).
  const SYNC_STALE_MS = 60000;
  // A stream that is still CONNECTING gets a grace window before the
  // watchdog tears it down: tab focus fires ensureEvents during page load,
  // and closing an in-flight connect is exactly the churn (visible as
  // NS_BINDING_ABORTED in devtools) this must avoid. A connect genuinely
  // stuck longer than the grace period (queued behind a dead transport)
  // still gets force-reconnected — the sweep makes that self-limiting.
  const CONNECT_GRACE_MS = 45000;
  let lastEventAt = 0;
  let connectingSince = 0;
  let everConnected = false;

  function scheduleSyncLoad() {
    if (editingId != null) {
      deferredSync = true;
      return;
    }
    clearTimeout(syncTimer);
    syncTimer = setTimeout(() => {
      load();
    }, 300);
  }

  function onSyncPoke() {
    if (Date.now() - api.lastMutationAt < 1500) return;
    scheduleSyncLoad();
  }

  function teardownEvents() {
    const es = eventSource;
    if (!es) return;
    eventSource = null;
    es.onerror = null;
    es.close();
  }

  // (Re)connect unless a healthy stream is already running: one that is
  // closed (fatal connect error), silent past the heartbeat window, or
  // stuck connecting past the grace period is torn down and replaced.
  function ensureEvents() {
    if (typeof EventSource === 'undefined') return;
    if (eventSource) {
      const state = eventSource.readyState;
      if (state === EventSource.OPEN) {
        if (Date.now() - lastEventAt <= SYNC_STALE_MS) return;
      } else if (state === EventSource.CONNECTING) {
        if (Date.now() - connectingSince <= CONNECT_GRACE_MS) return;
      }
      teardownEvents();
    }
    connectEvents();
  }

  function connectEvents() {
    const es = new EventSource('/api/events');
    eventSource = es;
    lastEventAt = Date.now();
    connectingSince = Date.now();
    // Any dispatch (open, sync, ping, even error) proves the socket is
    // alive; keep it that way for the staleness check above.
    const live = () => {
      if (eventSource === es) lastEventAt = Date.now();
    };
    es.onopen = () => {
      if (eventSource !== es) return;
      live();
      connectingSince = 0;
      // Anything can have changed while the stream was down (server
      // restart, sleep/wake): pokes from that window are gone forever, so
      // catch up with one load per reconnection.
      if (everConnected) scheduleSyncLoad();
      everConnected = true;
    };
    es.addEventListener('sync', () => {
      if (eventSource !== es) return;
      live();
      onSyncPoke();
    });
    es.addEventListener('ping', live);
    es.onerror = () => {
      if (eventSource !== es) return;
      // CONNECTING: the browser retries by itself (the server advertises a
      // 3s retry); a failing attempt dispatches errors, keeping the stream
      // off the staleness path so the native retry loop is left alone.
      // CLOSED is fatal — the connect itself was rejected (e.g. a session
      // cookie that lapsed while idle under an API key) and the browser
      // will never retry — so tear down and try again shortly.
      if (es.readyState !== EventSource.CLOSED) {
        live();
        return;
      }
      teardownEvents();
      setTimeout(ensureEvents, 15000);
    };
  }

  // Background tabs throttle timers, so a periodic sweep alone could run
  // minutes late — it is only the backstop. The moment the tab is visible
  // again, the window gains focus, or the network returns, the stream is
  // health-checked and reconnected immediately: none of those events are
  // throttled, and a frozen tab resumes through these same paths.
  let healthWired = false;
  function watchEvents() {
    if (typeof EventSource === 'undefined') return;
    ensureEvents();
    if (healthWired) return;
    healthWired = true;
    setInterval(ensureEvents, 30000);
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') ensureEvents();
    });
    window.addEventListener('focus', ensureEvents);
    window.addEventListener('online', ensureEvents);
  }

  offline.init({
    reload: () => load(),
    reproject,
  });

  return {
    get todos() {
      return todos;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get labels() {
      return labels;
    },
    get priorities() {
      return priorities;
    },
    get boards() {
      return boards;
    },
    get savedSearches() {
      return savedSearches;
    },
    get activeBoardId() {
      return activeBoardId;
    },
    get activeBoard() {
      return boardById(activeBoardId);
    },
    get editingId() {
      return editingId;
    },
    get rejectionTick() {
      return rejectionTick;
    },
    get contextMenu() {
      return contextMenu;
    },
    get filterText() {
      return filterText;
    },
    get filterActive() {
      return filterIsActive();
    },
    get filterError() {
      return filterError;
    },
    setFilterText(text) {
      applyFilterText(text);
    },
    clearFilter() {
      filterText = '';
      persistFilter();
      syncURL();
      clearTimeout(filterTimer);
      load();
    },
    isEditing(id) {
      return editingId === id;
    },
    setError(message) {
      error = message;
    },
    beginEdit,
    endEdit,
    watchEvents,
    markEditDirty,
    openContextMenu,
    closeContextMenu,
    childrenOf,
    visibleChildrenOf,
    byId,
    dropPosition,
    boardById,
    load,
    readURL: readURLParams,
    selectBoard,
    createBoard,
    renameBoard,
    reorderBoard,
    deleteBoard,
    create,
    update,
    setCompleted,
    remove,
    move,
    loadLabels,
    addPredefinedLabel,
    deleteLabel,
    updateLabelColor,
    loadPriorities,
    addPredefinedPriority,
    removePredefinedPriority,
    updatePriorityColor,
    reorderPriorities,
    createSavedSearch,
    deleteSavedSearch,
    applySavedSearch,
  };
}
