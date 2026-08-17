// Unit tests for the offline machinery in offline.svelte.js: canonical
// snapshots, clash detection, the optimistic projection, and the flush state
// machine (merge-dialog flows, agreeing completions, wedged-flag replays,
// cancelled creates, reconnect push-then-pull). Only the pieces that need a
// real browser and server — service worker, SSE, cookies, the actual DOM —
// stay in web/e2e/ (Playwright).
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Every api method rejects as a network error (no status) by default, so
// enqueueing keeps intents queued instead of replaying them anywhere.
const apiFns = vi.hoisted(() => ({
  getTodo: vi.fn(),
  createTodo: vi.fn(),
  updateTodo: vi.fn(),
  deleteTodo: vi.fn(),
  moveTodo: vi.fn(),
  completeTodo: vi.fn(),
  parseSchedule: vi.fn(),
  extractSchedule: vi.fn(),
}));
vi.mock('./api.js', () => ({ api: apiFns }));

import {
  _resetForTests,
  canonicalJson,
  detectClash,
  describeTarget,
  desiredValue,
  fieldChanged,
  fmtValue,
  formatRecurrence,
  isTempId,
  mineSummary,
  offline,
  safeJson,
  serverValue,
  snapshot,
} from './offline.svelte.js';

// A representative wire todo: every compared field populated.
const wireTodo = () => ({
  id: 7,
  boardId: 1,
  title: 'Report',
  description: 'quarterly',
  completed: false,
  labels: ['work', 'urgent'],
  priority: 'high',
  dueDate: '2026-08-20',
  recurrence: { frequency: 'weekly', interval: 1, weekdays: [1] },
  parentId: null,
  position: 2,
});

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllTimers();
  for (const fn of Object.values(apiFns)) {
    fn.mockReset();
    fn.mockImplementation(() => Promise.reject(new TypeError('fetch failed')));
  }
  _resetForTests();
});

// --- canonicalJson ---

describe('canonicalJson', () => {
  it('serialises primitives', () => {
    expect(canonicalJson(5)).toBe('5');
    expect(canonicalJson('x')).toBe('"x"');
    expect(canonicalJson(true)).toBe('true');
    expect(canonicalJson(null)).toBe('null');
    expect(canonicalJson(undefined)).toBe('null');
  });

  it('sorts object keys regardless of insertion order', () => {
    expect(canonicalJson({ b: 2, a: 1 })).toBe(canonicalJson({ a: 1, b: 2 }));
    expect(canonicalJson({ a: 1, b: 2 })).toBe('{"a":1,"b":2}');
  });

  it('sorts nested keys recursively', () => {
    expect(canonicalJson({ x: { d: 1, c: 2 }, y: 0 })).toBe(canonicalJson({ y: 0, x: { c: 2, d: 1 } }));
  });

  it('preserves array order', () => {
    expect(canonicalJson([2, 1])).toBe('[2,1]');
    expect(canonicalJson([2, 1])).not.toBe(canonicalJson([1, 2]));
  });
});

// --- snapshot ---

describe('snapshot', () => {
  it('fills defaults for missing fields', () => {
    expect(snapshot({})).toEqual({
      title: '',
      description: '',
      completed: false,
      labels: [],
      priority: '',
      dueDate: '',
      recurrence: '',
      parentId: null,
      position: 0,
    });
  });

  it('sorts labels without mutating the input', () => {
    const labels = ['b', 'a'];
    const snap = snapshot({ labels });
    expect(snap.labels).toEqual(['a', 'b']);
    expect(labels).toEqual(['b', 'a']);
  });

  it('canonicalises recurrence so key order cannot fake a change', () => {
    const a = snapshot({ recurrence: { frequency: 'daily', interval: 2 } });
    const b = snapshot({ recurrence: { interval: 2, frequency: 'daily' } });
    expect(a.recurrence).toBe(b.recurrence);
  });

  it('coerces completed to a boolean', () => {
    expect(snapshot({ completed: 1 }).completed).toBe(true);
    expect(snapshot({ completed: 0 }).completed).toBe(false);
  });
});

// --- normalisation used by clash detection ---

describe('desiredValue / serverValue / fieldChanged', () => {
  it('normalises labels identically on both sides', () => {
    const intent = { kind: 'update', patch: { labels: ['b', 'a'] } };
    expect(desiredValue(intent, 'labels')).toBe(['a', 'b'].join('\u0000'));
    expect(serverValue({ labels: ['a', 'b'] }, 'labels')).toBe(['a', 'b'].join('\u0000'));
  });

  it('reads completion from the intent, not the patch', () => {
    expect(desiredValue({ kind: 'complete', completed: true }, 'completed')).toBe(true);
    expect(desiredValue({ kind: 'complete', completed: false, patch: { completed: true } }, 'completed')).toBe(false);
  });

  it('canonicalises recurrence and blanks missing values', () => {
    const intent = { kind: 'update', patch: { recurrence: { b: 2, a: 1 } } };
    expect(desiredValue(intent, 'recurrence')).toBe(canonicalJson({ a: 1, b: 2 }));
    expect(desiredValue({ kind: 'update', patch: {} }, 'title')).toBe('');
  });

  it('compares arrays by joined content and scalars directly', () => {
    expect(fieldChanged({ labels: ['a'] }, { labels: ['a'] }, 'labels')).toBe(false);
    expect(fieldChanged({ labels: ['a'] }, { labels: ['a', 'b'] }, 'labels')).toBe(true);
    expect(fieldChanged({ title: 'x' }, { title: 'x' }, 'title')).toBe(false);
    expect(fieldChanged({ title: 'x' }, { title: 'y' }, 'title')).toBe(true);
  });
});

// --- merge-dialog rendering ---

describe('fmtValue / formatRecurrence', () => {
  it('renders labels and completion for humans', () => {
    expect(fmtValue('labels', ['a', 'b'])).toBe('a, b');
    expect(fmtValue('labels', [])).toBe('none');
    expect(fmtValue('completed', true)).toBe('done');
    expect(fmtValue('completed', false)).toBe('not done');
  });

  it('renders empty values as none', () => {
    expect(fmtValue('dueDate', '')).toBe('none');
    expect(fmtValue('dueDate', '2026-08-20')).toBe('2026-08-20');
    expect(fmtValue('title', '')).toBe('none');
    expect(fmtValue('priority', null)).toBe('none');
    expect(fmtValue('priority', 'high')).toBe('high');
  });

  it('renders a recognisable recurrence summary', () => {
    expect(formatRecurrence(null)).toBe('none');
    expect(formatRecurrence({})).toBe('none');
    expect(formatRecurrence({ frequency: 'weekly' })).toBe('every 1 weekly');
    expect(formatRecurrence({ frequency: 'monthly', interval: 2 })).toBe('every 2 monthly');
    expect(formatRecurrence({ frequency: 'daily', interval: 3, fromCompletion: true })).toBe(
      'every 3 daily (from completion)',
    );
    expect(formatRecurrence('{"frequency":"daily","interval":3}')).toBe('every 3 daily');
    expect(fmtValue('recurrence', { frequency: 'daily', interval: 3 })).toBe('every 3 daily');
  });
});

describe('safeJson', () => {
  it('parses valid JSON', () => {
    expect(safeJson('{"a":1}')).toEqual({ a: 1 });
  });
  it('returns null instead of throwing on garbage', () => {
    expect(safeJson('not json')).toBeNull();
  });
});

describe('describeTarget / mineSummary', () => {
  const base = { title: 'Report' };
  it('names what each intent kind does', () => {
    expect(describeTarget({ kind: 'create', payload: { title: 'Buy milk' } })).toBe('"Buy milk"');
    expect(describeTarget({ kind: 'create', payload: {}, base })).toBe('"Report"');
    expect(describeTarget({ kind: 'complete', completed: true, base })).toBe('marking "Report" done');
    expect(describeTarget({ kind: 'complete', completed: false, base })).toBe('marking "Report" not done');
    expect(describeTarget({ kind: 'delete', base })).toBe('deleting "Report"');
    expect(describeTarget({ kind: 'move', base })).toBe('moving "Report"');
    expect(describeTarget({ kind: 'update', base })).toBe('updating "Report"');
    expect(describeTarget({ kind: 'update' })).toBe('updating "a todo"');
  });

  it('summarises the local side of a conflict', () => {
    expect(mineSummary({ kind: 'complete', completed: true })).toBe('mark it done');
    expect(mineSummary({ kind: 'complete', completed: false })).toBe('mark it not done');
    expect(mineSummary({ kind: 'delete' })).toBe('delete it');
    expect(mineSummary({ kind: 'move' })).toBe('move it elsewhere');
    expect(mineSummary({ kind: 'update' })).toBe('your edit');
  });
});

describe('isTempId', () => {
  it('matches only negative ids', () => {
    expect(isTempId(-1)).toBe(true);
    expect(isTempId(0)).toBe(false);
    expect(isTempId(5)).toBe(false);
    expect(isTempId(null)).toBe(false);
    expect(isTempId(undefined)).toBe(false);
  });
});

// --- detectClash: the merge-dialog decision ---

describe('detectClash', () => {
  it('returns null when the server has not drifted', () => {
    const todo = wireTodo();
    const intent = { kind: 'update', id: 7, patch: { title: 'New' }, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(todo))).toBeNull();
  });

  it('ignores drift on fields the intent does not touch', () => {
    const todo = wireTodo();
    const drifted = { ...todo, description: 'changed elsewhere' };
    const intent = { kind: 'update', id: 7, patch: { title: 'New' }, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(drifted))).toBeNull();
  });

  it('ignores a server edit that already matches the intended outcome', () => {
    const todo = wireTodo();
    const bothEdited = { ...todo, title: 'New' };
    const intent = { kind: 'update', id: 7, patch: { title: 'New' }, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(bothEdited))).toBeNull();
  });

  it('flags a genuine edit clash with per-field rows', () => {
    const todo = wireTodo();
    const theirs = { ...todo, title: 'Theirs', labels: ['other'] };
    const intent = { kind: 'update', id: 7, patch: { title: 'Mine', labels: ['mine'] }, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(theirs))).toEqual({
      title: 'Report',
      summary: 'your edit',
      rows: [
        { label: 'Title', mine: 'Mine', theirs: 'Theirs' },
        { label: 'Labels', mine: 'mine', theirs: 'other' },
      ],
    });
  });

  it('treats agreeing completions as no conflict', () => {
    const todo = wireTodo(); // not done locally
    const doneElsewhere = { ...todo, completed: true };
    const intent = { kind: 'complete', id: 7, completed: true, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(doneElsewhere))).toBeNull();
  });

  it('flags opposite completions', () => {
    const todo = { ...wireTodo(), completed: true }; // was done
    const undoneElsewhere = { ...todo, completed: false };
    const intent = { kind: 'complete', id: 7, completed: true, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(undoneElsewhere))).toEqual({
      title: 'Report',
      summary: 'mark it done',
      rows: [{ label: 'Status', mine: 'done', theirs: 'not done' }],
    });
  });

  it('ignores sibling-position drift for moves', () => {
    const todo = wireTodo();
    const reordered = { ...todo, position: 9 };
    const intent = { kind: 'move', id: 7, parentId: null, position: 0, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(reordered))).toBeNull();
  });

  it('ignores a move when both sides chose the same parent', () => {
    const todo = wireTodo();
    const movedElsewhere = { ...todo, parentId: 5 };
    const intent = { kind: 'move', id: 7, parentId: 5, position: null, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(movedElsewhere))).toBeNull();
  });

  it('flags a move clash when the server reparented elsewhere', () => {
    const todo = wireTodo();
    const movedElsewhere = { ...todo, parentId: 5 };
    const intent = { kind: 'move', id: 7, parentId: 3, position: null, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(movedElsewhere))).toEqual({
      title: 'Report',
      summary: 'moving it elsewhere',
      rows: [{ label: 'Location', mine: 'moved while offline', theirs: 'moved on the server' }],
    });
  });

  it('lets a delete proceed when the server kept the todo unchanged', () => {
    const todo = wireTodo();
    const intent = { kind: 'delete', id: 7, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(todo))).toBeNull();
  });

  it('flags a delete when the server edited the todo', () => {
    const todo = wireTodo();
    const edited = { ...todo, title: 'Theirs', completed: true };
    const intent = { kind: 'delete', id: 7, base: snapshot(todo) };
    expect(detectClash(intent, snapshot(edited))).toEqual({
      title: 'Report',
      summary: 'deleting it',
      rows: [
        { label: 'Your change', mine: 'delete it', theirs: 'kept on the server' },
        { label: 'Title', mine: '(deleted)', theirs: 'Theirs' },
        { label: 'Status', mine: '(deleted)', theirs: 'done' },
      ],
    });
  });

  it('flags any non-delete intent for a todo deleted on the server', () => {
    const todo = wireTodo();
    const intent = { kind: 'update', id: 7, patch: { title: 'Mine' }, base: snapshot(todo) };
    expect(detectClash(intent, null)).toEqual({
      title: 'Report',
      deletedOnServer: true,
      summary: 'your edit',
      rows: [{ label: 'Your change', mine: 'your edit', theirs: '(deleted on the server)' }],
    });
    expect(detectClash({ kind: 'delete', id: 7, base: snapshot(todo) }, null)).toBeNull();
  });

  it('returns null for intents without a base snapshot', () => {
    expect(detectClash({ kind: 'update', id: 7, patch: {} }, snapshot(wireTodo()))).toBeNull();
  });
});

// --- optimistic projection (via the public enqueue surface) ---

describe('projection', () => {
  it('projects a queued create as a pending todo with defaults', () => {
    const tempId = offline.enqueueCreate({
      boardId: 1,
      payload: { title: 'Buy milk', labels: ['errand'], dueDate: '2026-08-21' },
    });
    expect(tempId).toBeLessThan(0);
    const out = offline.project([]);
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({
      id: tempId,
      boardId: 1,
      title: 'Buy milk',
      description: '',
      completed: false,
      parentId: null,
      labels: ['errand'],
      priority: '',
      dueDate: '2026-08-21',
      recurrence: null,
      pending: true,
    });
  });

  it('applies a queued update and marks the todo pending', () => {
    const todo = wireTodo();
    offline.enqueueUpdate(todo, { title: 'New', priority: null });
    const out = offline.project([todo]);
    expect(out).toHaveLength(1);
    expect(out[0].title).toBe('New');
    expect(out[0].priority).toBe('');
    expect(out[0].pending).toBe(true);
    // The server list is never mutated.
    expect(todo.title).toBe('Report');
  });

  it('projects completion and deletion', () => {
    const todo = wireTodo();
    offline.enqueueComplete(todo, true);
    expect(offline.project([todo])[0].completed).toBe(true);
    _resetForTests();
    offline.enqueueDelete(todo);
    expect(offline.project([todo])).toHaveLength(0);
  });

  it('projects a move to a new parent and position', () => {
    const todo = wireTodo();
    offline.enqueueMove(todo, { parentId: 3, position: 1 });
    const out = offline.project([todo]);
    expect(out[0].parentId).toBe(3);
    expect(out[0].position).toBe(1);
    expect(out[0].pending).toBe(true);
  });

  it('skips intents that target an unmapped temp id', () => {
    offline.enqueueUpdate({ ...wireTodo(), id: -4 }, { title: 'New' });
    const out = offline.project([]);
    expect(out).toHaveLength(0);
  });

  it('cancels a pending create on delete, dropping intents that reference it', () => {
    const tempId = offline.enqueueCreate({ boardId: 1, payload: { title: 'Temp' } });
    offline.enqueueUpdate({ ...wireTodo(), id: tempId }, { title: 'New' });
    offline.enqueueDelete({ id: tempId });
    expect(offline.queued).toHaveLength(0);
    expect(offline.project([])).toHaveLength(0);
  });

  it('re-parents queued subtask creates onto a cancelled temp parent', () => {
    const parent = offline.enqueueCreate({ boardId: 1, payload: { title: 'Parent' } });
    offline.enqueueCreate({ boardId: 1, parentId: parent, payload: { title: 'Child' } });
    offline.enqueueDelete({ id: parent });
    const out = offline.project([]);
    expect(out).toHaveLength(1);
    expect(out[0].title).toBe('Child');
    expect(out[0].parentId).toBeNull();
  });
});

// --- flush: only the pieces the projection depends on ---

describe('flush basics', () => {
  it('keeps intents queued when the network is down', async () => {
    offline.enqueueUpdate(wireTodo(), { title: 'Mine' });
    await offline.flushNow();
    expect(offline.queued).toHaveLength(1);
    expect(apiFns.updateTodo).not.toHaveBeenCalled();
  });

  it('replays a create and records the temp-to-real id mapping', async () => {
    apiFns.createTodo.mockImplementationOnce(async () => ({ id: 42 }));
    const tempId = offline.enqueueCreate({ boardId: 1, payload: { title: 'Buy milk' } });
    // The enqueue kicks off its own flush; let it settle before asserting.
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    expect(apiFns.createTodo).toHaveBeenCalledTimes(1);
    expect(apiFns.createTodo).toHaveBeenCalledWith(expect.objectContaining({ title: 'Buy milk', boardId: 1 }));
    expect(offline.queued).toHaveLength(0);
    // Once mapped, the create is no longer projected as a duplicate.
    expect(offline.project([])).toHaveLength(0);
    expect(offline.project([{ ...wireTodo(), id: 42, title: 'Buy milk' }])).toHaveLength(1);
    expect(tempId).toBeLessThan(0);
  });

  it('consumes an agreeing completion instead of replaying it', async () => {
    apiFns.getTodo.mockImplementationOnce(async () => ({ ...wireTodo(), completed: true }));
    offline.enqueueComplete(wireTodo(), true);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    expect(offline.queued).toHaveLength(0);
    expect(apiFns.completeTodo).not.toHaveBeenCalled();
  });
});

// --- flush state machine (moved out of the e2e suite, which now covers only
// what needs a real browser and server) ---

// The async flush awaits mocked api calls and merge-dialog resolutions, so a
// test settles it by pumping the (fake) microtask queue a few rounds.
async function settle(rounds = 8) {
  for (let i = 0; i < rounds; i++) await vi.advanceTimersByTimeAsync(0);
}

function initHooks() {
  const hooks = { reload: vi.fn(), reproject: vi.fn() };
  offline.init(hooks);
  return hooks;
}

describe('flush state machine', () => {
  it('cascade: an agreeing child completion is consumed, not replayed (no merge dialog)', async () => {
    // Parent and child are both completed offline. At replay time the
    // parent's completion has already cascaded server-side (see
    // TestSetCompletedCascade in internal/db), so the server holds the
    // child's outcome too: the child's own intent must be consumed instead of
    // replayed — a second complete call could spawn a second next instance of
    // a recurring todo — and identical values must not raise the dialog.
    apiFns.getTodo.mockImplementation(async (id) =>
      id === 8 ? { ...wireTodo(), id: 8, parentId: 7, completed: true } : { ...wireTodo(), completed: false },
    );
    apiFns.completeTodo.mockImplementation(async () => ({}));
    const hooks = initHooks();
    offline.enqueueComplete(wireTodo(), true);
    offline.enqueueComplete({ ...wireTodo(), id: 8, parentId: 7 }, true);
    await settle();
    expect(apiFns.completeTodo).toHaveBeenCalledTimes(1);
    expect(apiFns.completeTodo).toHaveBeenCalledWith(7, true);
    expect(offline.queued).toHaveLength(0);
    expect(offline.conflict).toBeNull();
    expect(offline.needsReview).toBe(false);
    expect(hooks.reload).toHaveBeenCalled();
  });

  it('flushes despite a wedged-false online flag; a replay heals it', async () => {
    // Linux can wedge navigator.onLine false forever while reads still work:
    // flush attempts must never consult the flag, and a successful replay is
    // proof of connectivity that forces it back true.
    apiFns.createTodo.mockImplementationOnce(async () => ({ id: 42 }));
    initHooks();
    window.dispatchEvent(new Event('offline'));
    expect(offline.online).toBe(false);
    offline.enqueueCreate({ boardId: 1, payload: { title: 'Wedged todo' } });
    await settle();
    expect(apiFns.createTodo).toHaveBeenCalledTimes(1);
    expect(offline.queued).toHaveLength(0);
    expect(offline.online).toBe(true);
  });

  it('merge dialog: a clashing server edit pauses the flush; keep-mine replays my edit', async () => {
    apiFns.getTodo.mockImplementation(async () => ({ ...wireTodo(), title: 'Server edited' }));
    apiFns.updateTodo.mockImplementation(async () => ({}));
    const hooks = initHooks();
    offline.enqueueUpdate(wireTodo(), { title: 'Mine edited' });
    await settle();
    // The flush is parked on the dialog: both sides shown, review needed,
    // nothing written yet.
    expect(offline.conflict).toBeTruthy();
    expect(offline.conflict.rows).toEqual([
      expect.objectContaining({ label: 'Title', mine: 'Mine edited', theirs: 'Server edited' }),
    ]);
    expect(offline.needsReview).toBe(true);
    expect(apiFns.updateTodo).not.toHaveBeenCalled();
    offline.resolveConflict('mine');
    await settle();
    expect(apiFns.updateTodo).toHaveBeenCalledWith(7, { title: 'Mine edited' });
    expect(offline.queued).toHaveLength(0);
    expect(offline.needsReview).toBe(false);
    expect(hooks.reload).toHaveBeenCalled();
  });

  it('merge dialog: decide later defers, focus does not reopen, badge reopen then keep-server drops my edit', async () => {
    apiFns.getTodo.mockImplementation(async () => ({ ...wireTodo(), title: 'Server again' }));
    const hooks = initHooks();
    offline.enqueueUpdate({ ...wireTodo(), title: 'Mine edited' }, { title: 'Second edit' });
    await settle();
    expect(offline.conflict).toBeTruthy();
    offline.deferConflict();
    await settle();
    // Deferred: the intent stays queued, no dialog, but review is needed.
    expect(offline.conflict).toBeNull();
    expect(offline.queued).toHaveLength(1);
    expect(offline.needsReview).toBe(true);
    // Focus/visibility auto-resyncs stay silent while a decision is deferred.
    const checks = apiFns.getTodo.mock.calls.length;
    window.dispatchEvent(new Event('focus'));
    await settle();
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(30000);
    expect(apiFns.getTodo.mock.calls.length).toBe(checks);
    // Reopening (the sync badge calls flushNow) re-raises the same clash…
    const done = offline.flushNow();
    await settle();
    expect(offline.conflict).toBeTruthy();
    expect(offline.conflict.rows[0]).toEqual(
      expect.objectContaining({ mine: 'Second edit', theirs: 'Server again' }),
    );
    // …and keep-server drops my queued edit in favour of the server's.
    offline.resolveConflict('theirs');
    await done;
    await settle();
    expect(apiFns.updateTodo).not.toHaveBeenCalled();
    expect(offline.queued).toHaveLength(0);
    expect(offline.needsReview).toBe(false);
    expect(hooks.reload).toHaveBeenCalled();
  });

  it('merge dialog: a server-side deletion raises a discard-only conflict', async () => {
    apiFns.getTodo.mockImplementation(async () => {
      throw Object.assign(new Error('not found'), { status: 404 });
    });
    const hooks = initHooks();
    offline.enqueueUpdate(wireTodo(), { title: 'Mine edited' });
    await settle();
    expect(offline.conflict?.deletedOnServer).toBe(true);
    expect(offline.conflict.rows[0].theirs).toBe('(deleted on the server)');
    offline.resolveConflict('theirs');
    await settle();
    expect(offline.queued).toHaveLength(0);
    expect(hooks.reload).toHaveBeenCalled();
  });

  it('never replays an offline create that was deleted before reconnect', async () => {
    const hooks = initHooks();
    offline.enqueueCreate({ boardId: 1, payload: { title: 'Cancel me' } });
    await settle(); // network is down: one failed attempt, the create stays queued
    expect(offline.queued).toHaveLength(1);
    expect(apiFns.createTodo).toHaveBeenCalledTimes(1);
    offline.enqueueDelete({ id: offline.queued[0].tempId });
    expect(offline.queued).toHaveLength(0);
    expect(offline.project([])).toHaveLength(0);
    // Reconnect (tab focus): nothing to push, but the confirming pull runs,
    // and even after the full backoff window the create is never attempted
    // again — it was cancelled, not replayed.
    window.dispatchEvent(new Event('focus'));
    await settle();
    await vi.advanceTimersByTimeAsync(30000);
    expect(apiFns.createTodo).toHaveBeenCalledTimes(1);
    expect(hooks.reload).toHaveBeenCalled();
  });

  it('reconnect pushes the queue first, then pulls exactly once', async () => {
    // The online event is a full push-then-pull resync: queued intents replay
    // and the reload (the pull that makes incoming foreign changes visible)
    // runs exactly once — from the flush when there was something to push,
    // from resync itself when the queue was empty.
    apiFns.createTodo.mockImplementationOnce(async () => ({ id: 42 }));
    const hooks = initHooks();
    offline.enqueueCreate({ boardId: 1, payload: { title: 'Pushed' } });
    await settle();
    expect(apiFns.createTodo).toHaveBeenCalledTimes(1);
    expect(hooks.reload).toHaveBeenCalledTimes(1);
    hooks.reload.mockClear();
    window.dispatchEvent(new Event('online'));
    await settle();
    expect(hooks.reload).toHaveBeenCalledTimes(1);
  });
});
