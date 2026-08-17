// First-link extraction for the todo row's link badge. Descriptions are
// markdown, so this walks marked's token stream — the same parser that
// renders the description — and returns the href of the first link token:
// markdown links, angle autolinks, and bare GFM autolinks alike, in render
// order (including inside lists). Only http(s) hrefs count, so relative or
// scripted destinations never become a clickable badge.
import { marked } from 'marked';

export function firstLink(description) {
  if (!description) return null;
  let found = null;
  const walk = (tokens) => {
    if (found) return;
    for (const token of tokens) {
      if (found) return;
      if (token.type === 'link' && /^https?:\/\//.test(token.href || '')) {
        found = token.href;
        return;
      }
      // Links nest inside emphasis and block structures; lists keep their
      // items under .items, everything else nests under .tokens.
      for (const key of ['tokens', 'items']) {
        if (Array.isArray(token[key])) walk(token[key]);
      }
    }
  };
  walk(marked.lexer(description));
  return found;
}

// Short badge label for a URL: the hostname, minus any www. prefix.
export function hostLabel(href) {
  try {
    return new URL(href).hostname.replace(/^www\./, '');
  } catch {
    return href;
  }
}
