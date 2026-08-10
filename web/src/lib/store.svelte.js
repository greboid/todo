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

export const store = createStore();

function createStore() {
  let todos = $state([]); // array of todo objects
  let loading = $state(false);
  let error = $state(null);
  let labels = $state([]);
  let boards = $state([]);
  // Active board is the one currently shown. Persisted across reloads; falls
  // back to the first board if the stored id no longer exists.
  let activeBoardId = $state(Number(storage.get('todo:activeBoard')) || null);

  // Single-edit enforcement: only one todo may be edited at a time.
  // editingId holds the id of the todo currently in edit mode (or null).
  // editingDirty becomes true once the user has modified any field; while a
  // dirty edit is open, attempts to edit a different todo are rejected and
  // rejectionTick is bumped so the editing todo can shake to signal refusal.
  let editingId = $state(null);
  let editingDirty = $state(false);
  let rejectionTick = $state(0);

  function byId(id) {
    return todos.find((t) => t.id === id);
  }

  // Collect a todo id and all of its descendant ids (transitively). Used by
  // completion cascade and deletion, both of which mirror the server's
  // recursive behaviour for snappier optimistic UI.
  function collectDescendants(id) {
    const childIndex = Map.groupBy(
      todos.filter((t) => t.parentId != null),
      (t) => t.parentId,
    );
    const affected = new Set([id]);
    for (const stack = [id]; stack.length;) {
      const kids = childIndex.get(stack.pop());
      if (!kids) continue;
      for (const { id: k } of kids) {
        if (!affected.has(k)) {
          affected.add(k);
          stack.push(k);
        }
      }
    }
    return affected;
  }

  function childrenOf(parentId) {
    // parentId === null means top-level roots.
    return todos
      .filter((t) => (parentId === null ? t.parentId == null : t.parentId === parentId))
      .sort(byPositionThenId);
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

  async function load() {
    loading = true;
    error = null;
    try {
      const boardList = (await api.listBoards()) ?? [];
      boards = boardList;
      // Reconcile persisted active board against the real list.
      if (!activeBoardId || !boardList.some((b) => b.id === activeBoardId)) {
        activeBoardId = boardList[0]?.id ?? null;
        if (activeBoardId) storage.set('todo:activeBoard', String(activeBoardId));
      }
      const [todoList, labelList] = await Promise.all([
        api.listTodos(activeBoardId ?? undefined),
        api.listLabels(),
      ]);
      todos = todoList ?? [];
      labels = labelList ?? [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function selectBoard(id) {
    if (id === activeBoardId) return;
    activeBoardId = id;
    storage.set('todo:activeBoard', String(id));
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
    // Server is source of truth for board ordering.
    boards = (await api.listBoards()) ?? [];
    return updated;
  }

  async function deleteBoard(id) {
    await api.deleteBoard(id);
    boards = boards.filter((b) => b.id !== id);
    if (activeBoardId === id) {
      activeBoardId = boards[0]?.id ?? null;
      if (activeBoardId) storage.set('todo:activeBoard', String(activeBoardId));
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

  async function create({ title, description = '', parentId = null, labels = [], dueDate = null, recurrence = null }) {
    // Top-level todos name the active board; subtasks inherit the parent's
    // board on the server (boardId omitted so the backend resolves it).
    const payload = {
      title,
      description,
      parentId: parentId ?? undefined,
      labels,
    };
    if (parentId == null) {
      payload.boardId = activeBoardId ?? undefined;
    }
    if (dueDate) payload.dueDate = dueDate;
    if (recurrence) payload.recurrence = recurrence;
    const created = await api.createTodo(payload);
    todos = [...todos, created];
    // Renumber siblings so client ordering stays in sync even if server did
    // anything fancy.
    renumberSiblings(created.parentId);
    return created;
  }

  async function update(id, patch) {
    const updated = await api.updateTodo(id, patch);
    applyUpdate(updated);
    if (patch.labels) {
      await loadLabels();
    }
    return updated;
  }

  // Optimistically cascade completion to descendants, then confirm via the
  // /complete endpoint which returns the refreshed full list.
  async function setCompleted(id, completed) {
    const cur = byId(id);
    if (!cur || cur.completed === completed) return;
    const affected = collectDescendants(id);
    for (const t of todos) {
      if (affected.has(t.id)) t.completed = completed;
    }
    try {
      const res = await api.completeTodo(id, completed);
      if (Array.isArray(res.todos)) {
        todos = res.todos;
      } else {
        applyUpdate(res.todo || { ...cur, completed });
      }
    } catch (e) {
      error = e.message;
      await load();
    }
  }

  function applyUpdate(updated) {
    const idx = todos.findIndex((t) => t.id === updated.id);
    if (idx >= 0) todos[idx] = updated;
  }

  async function remove(id) {
    const cur = byId(id);
    if (!cur) return;
    await api.deleteTodo(id);
    // Remove this todo and all its descendants (server cascades).
    const toDrop = collectDescendants(id);
    todos = todos.filter((t) => !toDrop.has(t.id));
    renumberSiblings(cur.parentId ?? null);
    await loadLabels();
  }

  async function move(id, { parentId, position }) {
    // Optimistically reorder locally, then confirm via API; on failure reload.
    const cur = byId(id);
    if (!cur) return;
    const targetParent = parentId === undefined ? cur.parentId : parentId;
    const siblings = childrenOf(targetParent ?? null).filter((t) => t.id !== id);
    const clamped = Math.max(0, Math.min(position ?? siblings.length, siblings.length));
    siblings.splice(clamped, 0, { ...cur, parentId: targetParent });
    siblings.forEach((t, i) => {
      const live = byId(t.id);
      if (live) {
        live.parentId = targetParent;
        live.position = i;
      }
    });
    try {
      const updated = await api.moveTodo(id, {
        parentId: targetParent === null ? null : targetParent,
        position: clamped,
      });
      applyUpdate(updated);
      // Server is source of truth for sibling ordering.
      renumberSiblings(targetParent ?? null);
      renumberSiblings(cur.parentId ?? null);
    } catch (e) {
      error = e.message;
      await load();
    }
  }

  function renumberSiblings(parentId) {
    childrenOf(parentId ?? null).forEach((t, i) => {
      t.position = i;
    });
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
    byId,
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
  };
}
