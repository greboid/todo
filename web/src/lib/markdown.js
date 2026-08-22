// Shared rendering of todo descriptions: GitHub-flavored markdown with hard
// line breaks, links forced into new tabs, and the output sanitized down to
// a small formatting whitelist. Used by the list's inline expansion and the
// detail page.
import { marked } from 'marked';
import DOMPurify from 'dompurify';

marked.use({
  gfm: true,
  breaks: true,
  renderer: {
    link({ href, tokens }) {
      const text = this.parser.parseInline(tokens);
      return `<a href="${href}" target="_blank" rel="noopener noreferrer">${text}</a>`;
    },
  },
});

export function renderDescription(md) {
  const raw = marked.parse(md ?? '', { async: false });
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: ['p', 'br', 'strong', 'em', 'del', 'code', 'pre', 'ul', 'ol', 'li', 'blockquote', 'a', 'hr'],
    ALLOWED_ATTR: ['href', 'target', 'rel'],
    ALLOW_DATA_ATTR: false,
  });
}
