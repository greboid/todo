// "2026-08-16" -> "Aug 16" (with the year when it differs from this one).
// Parsed with a time component so the date is read in local time, not UTC.
export function formatDay(day) {
  const d = new Date(`${day}T00:00:00`);
  const opts = { month: 'short', day: 'numeric' };
  if (d.getFullYear() !== new Date().getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString(undefined, opts);
}
