// Unit tests for label colours, detail routes, and server-ordered todo views.
import { describe, expect, it, vi } from 'vitest';

const apiFns = vi.hoisted(() => ({
  listBoards: vi.fn(async () => [{ id: 1, name: 'Board', position: 0 }]),
  listTodos: vi.fn(),
  listLabels: vi.fn(async () => []),
  listPriorities: vi.fn(async () => []),
  listSavedSearches: vi.fn(async () => []),
  getTodo: vi.fn(),
}));
vi.mock('./api.js', () => ({ api: apiFns }));

import { LABEL_PALETTE, labelColor, parseDetailPath, store } from './store.svelte.js';

describe('labelColor', () => {
  it('returns an explicit colour when one is set', () => {
    expect(labelColor('work', '#123456')).toBe('#123456');
    expect(labelColor('anything', '#abc')).toBe('#abc');
  });

  it('falls back to a palette colour for uncoloured labels', () => {
    for (const name of ['work', 'home', 'urgent', '']) {
      expect(LABEL_PALETTE).toContain(labelColor(name));
    }
  });

  it('is deterministic: the same name always maps to the same colour', () => {
    for (const name of ['work', 'home', 'errands', 'someday-maybe']) {
      expect(labelColor(name)).toBe(labelColor(name));
    }
    // Different names may collide, but a fixed sample spreads across the palette.
    const picks = new Set(['work', 'home', 'urgent', 'errands', 'reading', 'someday'].map((n) => labelColor(n)));
    expect(picks.size).toBeGreaterThanOrEqual(3);
  });
});

describe('server-ordered todo views', () => {
  // Deliberately differ from position/id order: views must preserve the
  // supplied array order, not independently reinterpret the server's order.
  const todos = [
    { id: 3, boardId: 1, parentId: null, position: 1 },
    { id: 1, boardId: 1, parentId: null, position: 0 },
    { id: 4, boardId: 1, parentId: 3, position: 1 },
    { id: 2, boardId: 1, parentId: 3, position: 0 },
  ];

  it('builds root and child views without re-sorting', async () => {
    apiFns.listTodos.mockResolvedValue(todos);
    await store.load();
    expect(store.error).toBeNull();
    expect(store.childrenOf(null).map((t) => t.id)).toEqual([3, 1]);
    expect(store.visibleChildrenOf(3).map((t) => t.id)).toEqual([4, 2]);
  });

  it('preserves fetched and offline fallback order on the detail page', async () => {
    apiFns.listTodos.mockResolvedValue(todos);
    await store.load();
    apiFns.getTodo.mockResolvedValue(todos[0]);
    store.openDetail(3);
    await vi.waitFor(() => expect(store.detailLoading).toBe(false));
    expect(store.detailChildren.map((t) => t.id)).toEqual([4, 2]);

    apiFns.getTodo.mockRejectedValue(new TypeError('offline'));
    store.openDetail(3);
    await vi.waitFor(() => expect(store.detailLoading).toBe(false));
    expect(store.detailChildren.map((t) => t.id)).toEqual([4, 2]);
  });
});

describe('parseDetailPath', () => {
  it('extracts the id from /todo/<id>', () => {
    expect(parseDetailPath('/todo/42')).toBe(42);
    expect(parseDetailPath('/todo/7/')).toBe(7);
  });

  it('accepts only positive integer ids', () => {
    expect(parseDetailPath('/todo/0')).toBeNull();
    expect(parseDetailPath('/todo/abc')).toBeNull();
    expect(parseDetailPath('/todo/')).toBeNull();
    expect(parseDetailPath('/todo/-3')).toBeNull();
    expect(parseDetailPath('/todo/1.5')).toBeNull();
  });

  it('rejects non-detail paths', () => {
    expect(parseDetailPath('/')).toBeNull();
    expect(parseDetailPath('/todos/42')).toBeNull();
    expect(parseDetailPath('/todo/42/more')).toBeNull();
    expect(parseDetailPath('/todox/42')).toBeNull();
    expect(parseDetailPath('')).toBeNull();
    expect(parseDetailPath(null)).toBeNull();
    expect(parseDetailPath(undefined)).toBeNull();
  });
});
