// Offline queue + optimistic projection for mutations made while the API is
// unreachable.
//
// Mutations are recorded as serialisable intents (persisted to localStorage,
// so they survive reloads) and projected optimistically onto the list the UI
// shows; todos whose fate is still queued render with a dashed outline. When
// connectivity returns the queue replays in order through the normal API
// code paths, so the server stays the source of truth for every write and
// offline degradation never reaches the database. Quick-add text is
// re-extracted and schedule text re-parsed by the *server* at replay time.
//
// Clashing incoming changes: every intent carries a snapshot ("base") of the
// todo as this client last saw it. Before replaying an intent that targets a
// todo the server already knows, the flush fetches the server's current
// version and compares it against the base for the fields the intent
// touches. If another device changed those fields while we were offline (or
// deleted the todo), the flush pauses and raises a conflict, which the store
// surfaces as a small merge dialog (keep my change / keep the server's).
//
// navigator.onLine is treated as a hint, not an oracle: flush attempts run
// regardless of the flag, and a successful replay is proof of connectivity
// and forces the flag true. A wedged-false flag therefore self-heals on the
// next interaction instead of queueing forever.

import { api } from './api.js';

const QUEUE_KEY = 'todo:offline-queue';
const IDMAP_KEY = 'todo:offline-idmap';

// Fields compared for clash detection, and how they read in the merge dialog.
const UPDATE_KEYS = ['title', 'description', 'labels', 'priority', 'dueDate', 'recurrence'];
const FIELD_LABELS = {
  title: 'Title',
  description: 'Description',
  labels: 'Labels',
  priority: 'Priority',
  dueDate: 'Due date',
  recurrence: 'Recurrence',
  completed: 'Status',
};

let queued = $state([]);
let idMap = $state({});
let online = $state(true);
let syncing = $state(false);
// Human-readable notes from the last flush (dropped intents and why).
let report = $state(null);
// Active merge-dialog model; while set, the flush is paused awaiting it.
let conflict = $state(null);
// True after the user defers a conflict: the queue is blocked on a decision
// and auto-retries stay silent (no dialog re-opening in their face) until
// they ask for it via the sync badge or connectivity changes.
let reviewDeferred = $state(false);

let nextTempId = -1;
let hooks = {};
let flushing = false;
let retryTimer = null;
let retryDelay = 2000;
let conflictWaiter = null;

// --- persistence ---

const storage = {
  get(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* ignore */
    }
  },
};

function persistQueue() {
  storage.set(QUEUE_KEY, JSON.stringify({ v: 1, intents: queued }));
}

function persistIdMap() {
  storage.set(IDMAP_KEY, JSON.stringify(idMap));
}

function restore() {
  const rawQueue = storage.get(QUEUE_KEY);
  if (rawQueue) {
    try {
      const parsed = JSON.parse(rawQueue);
      queued = Array.isArray(parsed?.intents) ? parsed.intents : [];
    } catch {
      queued = [];
    }
  }
  const rawMap = storage.get(IDMAP_KEY);
  if (rawMap) {
    try {
      const parsed = JSON.parse(rawMap);
      idMap = parsed && typeof parsed === 'object' ? parsed : {};
    } catch {
      idMap = {};
    }
  }
  for (const intent of queued) {
    if (intent?.tempId < nextTempId) nextTempId = intent.tempId;
  }
}
restore();

// --- helpers ---

function isTempId(id) {
  return id != null && id < 0;
}

// Resolve a todo reference to its real server id. Temp ids only appear here
// after their create has landed (replay records the mapping immediately), so
// an unmapped temp id means the create was cancelled — callers drop those.
function resolveId(id) {
  if (!isTempId(id)) return id;
  return idMap[id] ?? null;
}

// Parent references may point at todos created offline: map them to real
// ids at replay time. An orphaned reference (its create was cancelled)
// falls back to the root list rather than dropping the moved todo.
function resolveParent(parentId) {
  if (parentId == null || parentId >= 0) return parentId ?? null;
  return idMap[parentId] ?? null;
}

// Canonical comparable snapshot of a todo (works for wire todos and for the
// store's optimistically projected ones alike).
function snapshot(todo) {
  return {
    title: todo.title ?? '',
    description: todo.description ?? '',
    completed: !!todo.completed,
    labels: [...(todo.labels ?? [])].sort(),
    priority: todo.priority ?? '',
    dueDate: todo.dueDate ?? '',
    recurrence: todo.recurrence ? canonicalJson(todo.recurrence) : '',
    parentId: todo.parentId ?? null,
    position: todo.position ?? 0,
  };
}

// JSON.stringify with recursively sorted keys, so two objects describing
// the same value compare equal regardless of key order.
function canonicalJson(value) {
  if (value == null || typeof value !== 'object') return JSON.stringify(value ?? null);
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`;
  return `{${Object.keys(value)
    .sort()
    .map((k) => `${JSON.stringify(k)}:${canonicalJson(value[k])}`)
    .join(',')}}`;
}

// The value an intent wants a field to end up with, normalised the same way
// snapshot() normalises server todos, so "what we would write" can be
// compared against "what the server now has".
function desiredValue(intent, key) {
  const src = intent.kind === 'complete' ? { completed: intent.completed } : intent.patch ?? {};
  const v = src[key];
  switch (key) {
    case 'labels':
      return [...(v ?? [])].sort().join('\u0000');
    case 'completed':
      return !!v;
    case 'recurrence':
      return v ? canonicalJson(v) : '';
    default:
      return v ?? '';
  }
}

// The server snapshot's value for a field, normalised into the same shape
// desiredValue produces — labels are an array in the snapshot but a joined
// string in desiredValue, so comparing them directly would always look
// different and flag a conflict even when the server already holds the
// outcome this intent would write.
function serverValue(server, key) {
  if (key === 'labels') return [...(server[key] ?? [])].sort().join('\u0000');
  return server[key];
}

function fieldChanged(base, server, key) {
  const a = base[key];
  const b = server[key];
  if (Array.isArray(a) || Array.isArray(b)) {
    return (a ?? []).join('\u0000') !== (b ?? []).join('\u0000');
  }
  return a !== b;
}

function fmtValue(key, value) {
  switch (key) {
    case 'labels':
      return Array.isArray(value) && value.length ? value.join(', ') : 'none';
    case 'completed':
      return value ? 'done' : 'not done';
    case 'dueDate':
      return value || 'none';
    case 'recurrence':
      return formatRecurrence(value);
    default:
      return value == null || value === '' ? 'none' : String(value);
  }
}

// Minimal recurrence rendering for the merge dialog; the authoritative
// formatter lives server-side, this only needs to be recognisable.
function formatRecurrence(value) {
  if (!value) return 'none';
  const rule = typeof value === 'string' ? safeJson(value) : value;
  if (!rule || !rule.frequency) return 'none';
  return `every ${rule.interval || 1} ${rule.frequency}${rule.fromCompletion ? ' (from completion)' : ''}`;
}

function safeJson(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function describeTarget(intent) {
  const title = intent.base?.title ?? 'a todo';
  switch (intent.kind) {
    case 'create':
      return `"${intent.payload?.title ?? title}"`;
    case 'complete':
      return intent.completed ? `marking "${title}" done` : `marking "${title}" not done`;
    case 'delete':
      return `deleting "${title}"`;
    case 'move':
      return `moving "${title}"`;
    default:
      return `updating "${title}"`;
  }
}

// --- conflict detection ---

// Returns a conflict model for the merge dialog, or null when the intent can
// be replayed as-is. `server` is the server's current todo (already
// snapshotted) or null when it was deleted server-side.
function detectClash(intent, server) {
  const base = intent.base;
  if (!base) return null;

  if (server == null) {
    if (intent.kind === 'delete') return null; // already gone: nothing to merge
    return {
      title: base.title,
      deletedOnServer: true,
      summary: mineSummary(intent),
      rows: [{ label: 'Your change', mine: mineSummary(intent), theirs: '(deleted on the server)' }],
    };
  }

  let keys = [];
  if (intent.kind === 'update') {
    keys = UPDATE_KEYS.filter((k) => k in (intent.patch ?? {}) && fieldChanged(base, server, k));
    // A field the server moved to exactly the value this intent would set
    // (the same edit made from another client) is not a conflict: the
    // outcome is identical either way. Only fields that would end up
    // different are worth a dialog.
    keys = keys.filter((k) => desiredValue(intent, k) !== serverValue(server, k));
  } else if (intent.kind === 'complete') {
    // Same reasoning: another client completing the same todo (or the
    // server's completion cascade from a parent replayed earlier in this
    // flush) leaves the server already holding the outcome we want.
    keys = fieldChanged(base, server, 'completed') && !!intent.completed !== !!server.completed ? ['completed'] : [];
  } else if (intent.kind === 'move') {
    // Position drift from other clients reordering siblings is routine and
    // the server normalises it; only a different parent is a real clash —
    // and not even that when both sides moved it to the same place.
    if ((base.parentId ?? null) === (server.parentId ?? null)) return null;
    if ((intent.parentId ?? null) === (server.parentId ?? null)) return null;
    return {
      title: base.title,
      summary: 'moving it elsewhere',
      rows: [{ label: 'Location', mine: 'moved while offline', theirs: 'moved on the server' }],
    };
  } else if (intent.kind === 'delete') {
    keys = [...UPDATE_KEYS, 'completed'].filter((k) => fieldChanged(base, server, k));
    if (!keys.length) return null;
    return {
      title: base.title,
      summary: 'deleting it',
      rows: [
        { label: 'Your change', mine: 'delete it', theirs: 'kept on the server' },
        ...keys.map((k) => ({ label: FIELD_LABELS[k], mine: '(deleted)', theirs: fmtValue(k, server[k]) })),
      ],
    };
  } else {
    return null;
  }

  if (!keys.length) return null;
  const mine = intent.kind === 'update' ? intent.patch : { completed: intent.completed };
  return {
    title: base.title,
    summary: mineSummary(intent),
    rows: keys.map((k) => ({
      label: FIELD_LABELS[k] ?? k,
      mine: fmtValue(k, mine[k]),
      theirs: fmtValue(k, server[k]),
    })),
  };
}

function mineSummary(intent) {
  switch (intent.kind) {
    case 'complete':
      return intent.completed ? 'mark it done' : 'mark it not done';
    case 'delete':
      return 'delete it';
    case 'move':
      return 'move it elsewhere';
    default:
      return 'your edit';
  }
}

// --- enqueueing (called by the store after a direct API attempt failed at
// the network level, or for todos that only exist offline) ---

function pushIntent(intent) {
  queued = [...queued, intent];
  persistQueue();
  reproject();
  // Attempt a flush immediately: in the wedged-flag case (network up,
  // navigator.onLine wedged false) this very intent replays now; truly
  // offline it rejects fast and backs off. A deferred conflict blocks the
  // queue, so don't reopen that dialog unbidden.
  if (!reviewDeferred) flushNow();
}

function enqueueCreate({ boardId, parentId = null, payload, text = null }) {
  const tempId = --nextTempId;
  pushIntent({ kind: 'create', tempId, boardId, parentId, text, payload: { ...payload } });
  return tempId;
}

function enqueueUpdate(todo, patch) {
  pushIntent({ kind: 'update', id: todo.id, patch: { ...patch }, base: snapshot(todo) });
}

function enqueueComplete(todo, completed) {
  pushIntent({ kind: 'complete', id: todo.id, completed, base: snapshot(todo) });
}

function enqueueMove(todo, { parentId = null, position = null }) {
  pushIntent({ kind: 'move', id: todo.id, parentId, position, base: snapshot(todo) });
}

function enqueueDelete(todo) {
  if (isTempId(todo.id)) {
    // Deleting a todo that only exists offline cancels every queued intent
    // that references it, instead of replaying a create+delete pair.
    cancelTemp(todo.id);
    return;
  }
  pushIntent({ kind: 'delete', id: todo.id, base: snapshot(todo) });
}

function cancelTemp(tempId) {
  const kept = [];
  for (const intent of queued) {
    if (intent.kind === 'create' && intent.tempId === tempId) continue;
    if (intent.id === tempId) continue;
    // Anything that was to be filed under the cancelled todo falls back to
    // the root list rather than being lost.
    if (isTempId(intent.parentId) && intent.parentId === tempId) intent.parentId = null;
    if (intent.kind === 'create' && isTempId(intent.payload?.parentId) && intent.payload.parentId === tempId) {
      intent.payload.parentId = null;
    }
    kept.push(intent);
  }
  queued = kept;
  persistQueue();
  reproject();
}

// --- optimistic projection ---

// Applies every queued intent, in order, to the server's list to produce the
// view the UI shows. Purely display-side: the server re-derives the truth at
// replay. A malformed queued intent (e.g. left in localStorage by an older
// build) is a poison pill: drop the whole queue and report, and never let it
// reject load().
function project(serverTodos) {
  try {
    return projectInner(serverTodos ?? []);
  } catch {
    queued = [];
    idMap = {};
    nextTempId = -1;
    persistQueue();
    persistIdMap();
    report = 'Saved offline changes were unreadable and have been discarded.';
    return serverTodos ?? [];
  }
}

function projectInner(serverTodos) {
  const list = serverTodos.map((t) => ({ ...t }));
  const find = (id) => list.find((x) => x.id === id || (isTempId(id) && resolveId(id) === x.id));

  for (const intent of queued) {
    switch (intent.kind) {
      case 'create': {
        // Once the create has landed (id mapped) but the confirming reload
        // hasn't run yet, the server list already contains the real todo.
        if (intent.tempId != null && resolveId(intent.tempId) != null) break;
        const parentId = intent.parentId ?? intent.payload.parentId ?? null;
        list.push({
          id: intent.tempId,
          boardId: intent.boardId ?? intent.payload.boardId ?? null,
          title: intent.payload.title ?? '',
          description: intent.payload.description ?? '',
          completed: false,
          parentId,
          position: Number.MAX_SAFE_INTEGER,
          labels: [...(intent.payload.labels ?? [])],
          priority: intent.payload.priority ?? '',
          dueDate: intent.payload.dueDate ?? '',
          recurrence: intent.payload.recurrence ?? null,
          pending: true,
        });
        break;
      }
      case 'update': {
        const todo = find(intent.id);
        if (!todo) break;
        const { title, description, labels, priority, dueDate, recurrence } = intent.patch ?? {};
        if (title != null) todo.title = title;
        if (description != null) todo.description = description;
        if (labels != null) todo.labels = [...labels];
        if (priority !== undefined) todo.priority = priority ?? '';
        if (dueDate !== undefined) todo.dueDate = dueDate ?? '';
        if (recurrence !== undefined) todo.recurrence = recurrence ?? null;
        todo.pending = true;
        break;
      }
      case 'complete': {
        const todo = find(intent.id);
        if (!todo) break;
        todo.completed = !!intent.completed;
        todo.pending = true;
        break;
      }
      case 'delete': {
        const idx = list.findIndex((x) => x.id === intent.id || (isTempId(intent.id) && resolveId(intent.id) === x.id));
        if (idx >= 0) list.splice(idx, 1);
        break;
      }
      case 'move': {
        const todo = find(intent.id);
        if (!todo) break;
        todo.parentId = intent.parentId ?? null;
        if (intent.position != null) todo.position = intent.position;
        todo.pending = true;
        break;
      }
      default:
        throw new Error(`unknown intent kind: ${intent?.kind}`);
    }
  }
  return list;
}

// --- replay ---

async function replay(intent) {
  switch (intent.kind) {
    case 'create': {
      let payload = { ...intent.payload };
      if (intent.text) {
        // The quick-add grammar is re-evaluated by the server at replay time
        // so it can never drift from the Go source of truth.
        const res = await api.extractSchedule(intent.text);
        if (!res?.ok) {
          const err = new Error(res?.error || 'could not parse the saved text');
          err.status = 400;
          throw err;
        }
        payload = { title: res.title };
        if (res.labels?.length) payload.labels = res.labels;
        if (res.priority) payload.priority = res.priority;
        if (res.dueDate) payload.dueDate = res.dueDate;
        if (res.recurrence) payload.recurrence = res.recurrence;
      }
      const parentId = resolveParent(intent.parentId ?? payload.parentId);
      delete payload.parentId;
      if (parentId == null) {
        // Root todos carry their board explicitly; subtasks inherit the
        // parent's board server-side.
        payload.boardId = intent.boardId ?? payload.boardId;
      } else {
        payload.parentId = parentId;
      }
      const created = await api.createTodo(payload);
      if (intent.tempId != null) {
        idMap = { ...idMap, [intent.tempId]: created.id };
        persistIdMap();
      }
      return;
    }
    case 'update': {
      let patch = { ...intent.patch };
      // Edits saved offline may carry the raw free-text schedule instead of
      // a parse (the grammar endpoint was unreachable): parse it now, on the
      // server, exactly like the edit form would have.
      if (patch.rawSchedule != null) {
        const res = await api.parseSchedule(patch.rawSchedule);
        if (!res?.ok) {
          const err = new Error(res?.error || 'could not parse the schedule text');
          err.status = 400;
          throw err;
        }
        patch.dueDate = res.dueDate ?? null;
        patch.recurrence = res.recurrence ?? null;
      }
      delete patch.rawSchedule;
      await api.updateTodo(requireRealId(intent.id), patch);
      return;
    }
    case 'complete':
      await api.completeTodo(requireRealId(intent.id), intent.completed);
      return;
    case 'delete':
      await api.deleteTodo(requireRealId(intent.id));
      return;
    case 'move': {
      const body = { parentId: resolveParent(intent.parentId) };
      if (intent.position != null) body.position = intent.position;
      await api.moveTodo(requireRealId(intent.id), body);
      return;
    }
    default:
      throw new Error(`unknown intent kind: ${intent?.kind}`);
  }
}

function requireRealId(id) {
  const real = resolveId(id);
  if (real == null) {
    const err = new Error('the todo no longer exists');
    err.status = 404;
    throw err;
  }
  return real;
}

// --- flush ---

// Replays the queue. Returns true when it already triggered the confirming
// reload (anything left the queue — replayed, resolved, or dropped), so the
// caller knows whether a separate pull is still needed.
async function flushNow() {
  if (flushing) return false;
  if (!queued.length) return false;
  flushing = true;
  syncing = true;
  const notes = [];
  const startLen = queued.length;
  try {
    while (queued.length) {
      const intent = queued[0];
      if (!intent || typeof intent !== 'object' || !intent.kind) {
        consume(intent);
        continue;
      }

      // Drop intents whose temp todo never landed (cancelled create that a
      // later intent still referenced after a mid-replay reload).
      if (intent.kind !== 'create' && isTempId(intent.id) && resolveId(intent.id) == null) {
        consume(intent);
        continue;
      }

      // Clash detection: compare the server's current todo against the base
      // snapshot recorded at enqueue time, for real (already-synced) todos.
      if (intent.base && !isTempId(intent.id)) {
        let server = undefined; // undefined = unknown (skip check), null = deleted
        try {
          server = snapshot(await api.getTodo(intent.id));
        } catch (e) {
          if (e.status === 404) server = null;
          else if (e.status === undefined) break; // network: retry later
          // Other HTTP errors: the server is reachable; fall through and let
          // the replay itself surface the rejection.
        }
        if (server === null && intent.kind === 'delete') {
          // Both sides deleted it: ours is already done.
          consume(intent);
          continue;
        }
        if (server !== undefined) {
          // keepMine: the user already chose "keep my changes" for this
          // intent (a previous replay attempt was interrupted), so the
          // decision sticks instead of re-opening the dialog on retry.
          const clash = intent.keepMine ? null : detectClash(intent, server);
          if (clash) {
            conflict = clash;
            reviewDeferred = false;
            const choice = await waitForResolution();
            // The queue may have changed while the dialog was open (e.g. the
            // user deleted the pending todo); consume by identity, never by
            // position.
            if (!queued.includes(intent)) continue;
            if (choice === 'later') break; // keep queued; badge shows the block
            if (choice === 'theirs') {
              // Server version wins: drop ours.
              consume(intent);
              reproject();
              continue;
            }
            // 'mine': remember the decision and fall through to replay on
            // top of the server's version. The intent is only consumed by
            // the replay itself landing (or being rejected by the server) —
            // a network failure mid-flush keeps it queued for the next
            // attempt instead of silently dropping the change.
            intent.keepMine = true;
            persistQueue();
          } else if (intent.kind === 'complete' && server != null && !!server.completed === !!intent.completed) {
            // The server already holds this outcome (the same completion made
            // from another client, or the cascade from a parent completed
            // earlier in this flush): consume instead of replaying, so a
            // recurring todo can't spawn a second next instance.
            consume(intent);
            continue;
          }
        }
      }

      try {
        await replay(intent);
        consume(intent);
        retryDelay = 2000;
        // A successful replay is proof of connectivity, whatever the flag says.
        online = true;
      } catch (e) {
        if (e.status === undefined) {
          online = false;
          break; // network died mid-flush: keep the remainder queued
        }
        // The server rejected this intent (4xx): drop it and surface why,
        // then keep draining the rest.
        consume(intent);
        notes.push(`${describeTarget(intent)}: ${e.message}`);
        continue;
      }
    }
  } finally {
    flushing = false;
    syncing = false;
  }
  // Anything leaving the queue (replayed, conflict-resolved, or dropped
  // after a server rejection) changes what the list should show: re-derive
  // the projection and reload, so the optimistic view is replaced with the
  // server's authoritative one as soon as sync lands.
  const changed = queued.length !== startLen;
  if (changed) {
    report = null;
    reviewDeferred = false;
    // Pull first, reproject second: a successful reload already reflects
    // everything just replayed, so the optimistic view is replaced directly
    // by the authoritative one. Reprojecting first would render the stale
    // pre-replay serverTodos — a resolved "keep mine" merge visibly
    // reverting to the server's old value until the reload lands.
    await hooks.reload?.();
    reproject();
  }
  if (notes.length) report = notes.join('\n');
  if (queued.length && !reviewDeferred) scheduleRetry();
  return changed;
}

// Consume one intent by identity: the flush awaits network calls and merge
// dialogs, during which the user can keep mutating (and cancel-temp can
// remove intents), so position-based removal would eat the wrong item.
function consume(intent) {
  queued = queued.filter((i) => i !== intent);
  persistQueue();
}

function waitForResolution() {
  return new Promise((resolve) => {
    conflictWaiter = resolve;
  });
}

function settleConflict(choice) {
  const waiter = conflictWaiter;
  conflictWaiter = null;
  conflict = null;
  waiter?.(choice);
}

// --- retry/backoff ---

function scheduleRetry() {
  if (retryTimer || flushing || !queued.length || conflict) return;
  retryTimer = setTimeout(() => {
    retryTimer = null;
    retryDelay = Math.min(retryDelay * 2, 30000);
    flushNow();
  }, retryDelay);
}

// Full reconnect sync: push the offline queue first, then pull the
// server's current state. Incoming changes (made from another client while
// this one was offline, or while the tab sat idle) only reach the UI
// through load(), so a reconnect re-fetches even when there was nothing to
// push — otherwise those changes wait for a manual refresh.
async function resync() {
  clearTimeout(retryTimer);
  retryTimer = null;
  retryDelay = 2000;
  const pushed = await flushNow();
  if (!pushed) await hooks.reload?.();
}

function reproject() {
  hooks.reproject?.();
}

// --- lifecycle ---

function init({ reload, reproject }) {
  hooks = { reload, reproject };
}

if (typeof window !== 'undefined') {
  if (typeof navigator !== 'undefined') online = navigator.onLine !== false;
  window.addEventListener('online', () => {
    online = true;
    resync();
  });
  window.addEventListener('offline', () => {
    online = false;
  });
  // The service worker reports observed reachability of its network-first
  // fetches; that beats navigator.onLine under throttling and dead routes.
  navigator.serviceWorker?.addEventListener('message', (e) => {
    if (e.data?.type !== 'todo-network') return;
    if (e.data.up && !online) {
      online = true;
      resync();
    } else {
      online = e.data.up;
    }
  });
  // Background tabs throttle timers, and wake-from-sleep can leave the
  // online state wedged; re-attempt when the tab comes back. Focus and
  // visibility also pull fresh server state, so changes made in other
  // clients while this tab was idle appear without a manual refresh.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && !reviewDeferred) resync();
  });
  window.addEventListener('focus', () => {
    if (!reviewDeferred) resync();
  });
}

export const offline = {
  get queued() {
    return queued;
  },
  get online() {
    return online;
  },
  get syncing() {
    return syncing;
  },
  get report() {
    return report;
  },
  get conflict() {
    return conflict;
  },
  get needsReview() {
    return reviewDeferred || conflict != null;
  },
  isTempId,
  init,
  enqueueCreate,
  enqueueUpdate,
  enqueueComplete,
  enqueueDelete,
  enqueueMove,
  project,
  flushNow,
  // Merge-dialog actions. 'mine' replays the queued change, 'theirs' drops
  // it in favour of the server's version.
  resolveConflict: (choice) => settleConflict(choice),
  deferConflict: () => {
    reviewDeferred = true;
    settleConflict('later');
  },
  // Dismiss the "Not synced" notice once the user has read why intents were
  // dropped (the full note lives in the badge tooltip).
  acknowledgeReport: () => {
    report = null;
  },
};
