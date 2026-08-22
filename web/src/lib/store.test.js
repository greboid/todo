// Unit tests for the pure label-colour assignment shared by label chips,
// and the detail-page route parser.
import { describe, expect, it } from 'vitest';
import { LABEL_PALETTE, labelColor, parseDetailPath } from './store.svelte.js';

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
