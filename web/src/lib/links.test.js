// Unit tests for first-link extraction (the todo row's link badge).
import { describe, expect, it } from 'vitest';
import { firstLink, hostLabel } from './links.js';

describe('firstLink', () => {
  it('returns null for empty or link-free descriptions', () => {
    expect(firstLink('')).toBeNull();
    expect(firstLink(null)).toBeNull();
    expect(firstLink(undefined)).toBeNull();
    expect(firstLink('just words, no links')).toBeNull();
  });

  it('finds a markdown link', () => {
    expect(firstLink('see [the docs](https://example.com/docs) for more')).toBe(
      'https://example.com/docs',
    );
  });

  it('finds a bare GFM autolink and drops trailing punctuation', () => {
    expect(firstLink('go to https://example.com/page.')).toBe('https://example.com/page');
  });

  it('finds an angle-bracket autolink', () => {
    expect(firstLink('see <https://example.com>')).toBe('https://example.com');
  });

  it('keeps markdown-link destinations with balanced parens', () => {
    expect(firstLink('[wiki](https://en.wikipedia.org/wiki/Foo_(bar))')).toBe(
      'https://en.wikipedia.org/wiki/Foo_(bar)',
    );
  });

  it('returns the first link in render order across forms', () => {
    expect(firstLink('first https://a.com then [md](https://b.com)')).toBe('https://a.com');
    expect(firstLink('[md](https://b.com) then https://a.com')).toBe('https://b.com');
  });

  it('finds links inside lists and emphasis', () => {
    expect(firstLink('- item\n  - nested with https://a.com')).toBe('https://a.com');
    expect(firstLink('**see [docs](https://a.com)**')).toBe('https://a.com');
  });

  it('skips non-http(s) destinations', () => {
    expect(firstLink('[x](javascript:alert(1)) and [y](/relative) and mailto:a@b.com')).toBeNull();
    expect(firstLink('[x](javascript:alert(1)) but https://a.com')).toBe('https://a.com');
  });
});

describe('hostLabel', () => {
  it('shows the hostname without scheme or path', () => {
    expect(hostLabel('https://example.com/some/path?x=1')).toBe('example.com');
  });

  it('strips a www. prefix', () => {
    expect(hostLabel('https://www.example.com/')).toBe('example.com');
  });

  it('falls back to the input when the URL will not parse', () => {
    expect(hostLabel('not a url')).toBe('not a url');
  });
});
