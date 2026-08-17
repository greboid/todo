// Sort-token parsing for the filter bar. Kept as a standalone pure module so
// it is unit-testable; the authoritative sort grammar lives server-side in
// internal/filter (Go).

// Parse sort: tokens out of filter text for display. Returns an array of
// { field, desc, raw } in the order they appear (that order is the
// tie-break priority: first wins, subsequent break ties).
export function activeSorts(text) {
  const out = [];
  const re = /(?:^|\s)sort(?:by)?:(!?)(priority|p|label|l|date|due|duedate)\b/gi;
  let m;
  while ((m = re.exec(text)) !== null) {
    const field = m[2].toLowerCase();
    const canon =
      field === 'p' ? 'priority' : field === 'l' ? 'label' : field === 'due' || field === 'duedate' ? 'date' : field;
    out.push({ field: canon, desc: m[1] === '!', raw: m[0].trim() });
  }
  return out;
}
