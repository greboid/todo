// Central reactive store. Lives in a .svelte.js module so Svelte 5 runes
// ($state, $derived) work both here and in components that import it.
import { api } from './api.js';

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
  // todos holds the server's response for the active board + filter: every
  // matching todo plus the ancestors that keep the tree connected. The store
  // does no client-side filtering — the grammar lives in internal/filter.
  let todos = $state([]);
  let loading = $state(false);
  let error = $state(null);
  let labels = $state([]);
  let priorities = $state([]);
  let boards = $state([]);
  // Active board is the one currently shown. Persisted across reloads; falls
  // back to the first board if the stored id no longer exists. May also be set
  // from the URL (?board=<id>) which takes precedence over the stored value.
  let activeBoardId = $state(Number(storage.get('todo:activeBoard')) || null);

  // Single-edit enforcement: only one todo may be edited at a time.
  let editingId = $state(null);
  let editingDirty = $state(false);
  let rejectionTick = $state(0);

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

  // --- URL sync ---
  // board and filter are mirrored into the query string so views are
  // shareable/bookmarkable. replaceState avoids polluting browser history.
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

  function syncURL() {
    try {
      const params = new URLSearchParams(window.location.search);
      let changed = false;
      const boardVal = activeBoardId != null ? String(activeBoardId) : '';
      if (params.get('board') !== boardVal) {
        if (boardVal) params.set('board', boardVal);
        else params.delete('board');
        changed = true;
      }
      if (params.get('filter') !== filterText) {
        if (filterText) params.set('filter', filterText);
        else params.delete('filter');
        changed = true;
      }
      if (changed) {
        const qs = params.toString();
        const url = qs ? `?${qs}` : window.location.pathname;
        window.history.replaceState(null, '', url);
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
  }

  function markEditDirty() {
    if (editingId != null) editingDirty = true;
  }

  // (Re)fetch boards, the filtered todo list for the active board, and labels.
  // A 400 means the filter is invalid: surface it as filterError and keep the
  // last good list rather than clearing it.
  async function load() {
    readURLParams();
    loading = true;
    error = null;
    try {
      const boardList = (await api.listBoards()) ?? [];
      boards = boardList;
      if (!activeBoardId || !boardList.some((b) => b.id === activeBoardId)) {
        activeBoardId = boardList[0]?.id ?? null;
        if (activeBoardId) storage.set('todo:activeBoard', String(activeBoardId));
      }
      const [todoList, labelList, priorityList] = await Promise.all([
        api.listTodos(activeBoardId ?? undefined, filterText, todayISO()),
        api.listLabels(),
        api.listPriorities(),
      ]);
      todos = todoList ?? [];
      labels = labelList ?? [];
      priorities = priorityList ?? [];
      filterError = '';
      syncURL();
    } catch (e) {
      if (e.status === 400) {
        filterError = e.message;
      } else {
        error = e.message;
      }
    } finally {
      loading = false;
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

  async function create({ title, description = '', parentId = null, labels = [], priority = '', dueDate = null, recurrence = null }) {
    const payload = {
      title,
      description,
      parentId: parentId ?? undefined,
      labels,
    };
    if (priority) payload.priority = priority;
    if (parentId == null) {
      payload.boardId = activeBoardId ?? undefined;
    }
    if (dueDate) payload.dueDate = dueDate;
    if (recurrence) payload.recurrence = recurrence;
    const created = await api.createTodo(payload);
    // The new todo may or may not match the active filter; re-fetch to reflect
    // the server's filtered view.
    await load();
    return created;
  }

  async function update(id, patch) {
    const updated = await api.updateTodo(id, patch);
    if (patch.labels) {
      await loadLabels();
    }
    if (patch.priority !== undefined) {
      await loadPriorities();
    }
    // A field change (labels, priority, due date, title) can alter filter membership.
    await load();
    return updated;
  }

  // Toggle completion via the /complete endpoint, then re-fetch the filtered
  // view. Under the default "!has:complete" filter the completed todo leaves
  // the list; a completed recurring todo's spawned next instance appears.
  async function setCompleted(id, completed) {
    const cur = byId(id);
    if (!cur || cur.completed === completed) return;
    try {
      await api.completeTodo(id, completed);
      await load();
    } catch (e) {
      error = e.message;
      await load();
    }
  }

  async function remove(id) {
    const cur = byId(id);
    if (!cur) return;
    await api.deleteTodo(id);
    await loadLabels();
    await load();
  }

  async function move(id, { parentId, position }) {
    const cur = byId(id);
    if (!cur) return;
    const targetParent = parentId === undefined ? cur.parentId ?? null : parentId;
    const body = { parentId: targetParent };
    if (position != null) body.position = position;
    try {
      await api.moveTodo(id, body);
      await load();
    } catch (e) {
      error = e.message;
      await load();
    }
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

  async function removePredefinedLabel(name) {
    await api.removePredefinedLabel(name);
    await loadLabels();
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
      filterText = text;
      persistFilter();
      syncURL();
      // Debounce: typing fires one re-fetch after the user pauses.
      clearTimeout(filterTimer);
      filterTimer = setTimeout(() => {
        load();
      }, 200);
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
    markEditDirty,
    childrenOf,
    visibleChildrenOf,
    byId,
    dropPosition,
    boardById,
    load,
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
    removePredefinedLabel,
    updateLabelColor,
    loadPriorities,
    addPredefinedPriority,
    removePredefinedPriority,
    updatePriorityColor,
  };
}
