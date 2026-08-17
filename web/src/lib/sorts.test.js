// Unit tests for sort-token parsing (the filter bar's active-sort chips).
import { describe, expect, it } from 'vitest';
import { activeSorts } from './sorts.js';

describe('activeSorts', () => {
  it('returns nothing for text without sort tokens', () => {
    expect(activeSorts('')).toEqual([]);
    expect(activeSorts('!has:complete label:work search words')).toEqual([]);
    expect(activeSorts('resort:priority')).toEqual([]);
  });

  it('parses a bare sort token', () => {
    expect(activeSorts('sort:priority')).toEqual([{ field: 'priority', desc: false, raw: 'sort:priority' }]);
  });

  it('finds tokens among other filter text', () => {
    expect(activeSorts('!has:complete label:work sort:date report')).toEqual([
      { field: 'date', desc: false, raw: 'sort:date' },
    ]);
  });

  it('requires a word boundary after the field', () => {
    expect(activeSorts('sort:priorityx')).toEqual([]);
    expect(activeSorts('sort:priority:')).toEqual([{ field: 'priority', desc: false, raw: 'sort:priority' }]);
  });

  it('canonicalises field aliases', () => {
    expect(activeSorts('sort:p')[0].field).toBe('priority');
    expect(activeSorts('sort:l')[0].field).toBe('label');
    expect(activeSorts('sort:due')[0].field).toBe('date');
    expect(activeSorts('sort:duedate')[0].field).toBe('date');
    expect(activeSorts('sort:date')[0].field).toBe('date');
  });

  it('marks a ! prefix as descending', () => {
    expect(activeSorts('sort:!label')).toEqual([{ field: 'label', desc: true, raw: 'sort:!label' }]);
  });

  it('keeps appearance order across repeats and variants', () => {
    expect(
      activeSorts('sort:!p sort:l sortby:date sort:duedate'),
    ).toEqual([
      { field: 'priority', desc: true, raw: 'sort:!p' },
      { field: 'label', desc: false, raw: 'sort:l' },
      { field: 'date', desc: false, raw: 'sortby:date' },
      { field: 'date', desc: false, raw: 'sort:duedate' },
    ]);
  });

  it('matches case-insensitively and trims the leading boundary', () => {
    expect(activeSorts('SORT:LABEL')[0].field).toBe('label');
    expect(activeSorts('x SortBy:P')[0]).toEqual({ field: 'priority', desc: false, raw: 'SortBy:P' });
  });
});
