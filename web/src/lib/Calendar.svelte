<script>
  // Month-grid date picker used by the defer action. Emits the picked date as
  // a local-time ISO string (YYYY-MM-DD). Mounts fresh per open, so the view
  // starts on the selected date's month (or the current month when unset).
  import Icon from './Icon.svelte';
  import { untrack } from 'svelte';

  let { selected = '', onPick } = $props();

  function parseISO(s) {
    const [y, m, d] = (s || '').split('-').map(Number);
    if (!y || !m || !d) return null;
    return new Date(y, m - 1, d);
  }

  function isoOf(d) {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }

  // The component mounts fresh per open, so intentionally capture only the
  // initial value of `selected` to seed the visible month.
  const initial = untrack(() => parseISO(selected) ?? new Date());
  let viewYear = $state(initial.getFullYear());
  let viewMonth = $state(initial.getMonth());
  const todayIso = isoOf(new Date());

  const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];

  // Always six weeks so the grid height never jumps between months.
  const cells = $derived.by(() => {
    const offset = (new Date(viewYear, viewMonth, 1).getDay() + 6) % 7;
    const start = new Date(viewYear, viewMonth, 1 - offset);
    const out = [];
    for (let i = 0; i < 42; i++) {
      const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
      out.push({ iso: isoOf(d), day: d.getDate(), inMonth: d.getMonth() === viewMonth });
    }
    return out;
  });

  const monthLabel = $derived(
    new Date(viewYear, viewMonth, 1).toLocaleDateString(undefined, { month: 'long', year: 'numeric' }),
  );

  function shiftMonth(delta) {
    const d = new Date(viewYear, viewMonth + delta, 1);
    viewYear = d.getFullYear();
    viewMonth = d.getMonth();
  }
</script>

<div class="calendar" role="group" aria-label="Choose a date">
  <div class="cal-head">
    <button type="button" class="nav" onclick={() => shiftMonth(-1)} aria-label="Previous month">
      <Icon name="chevron" size={14} />
    </button>
    <span class="cal-title">{monthLabel}</span>
    <button type="button" class="nav" onclick={() => shiftMonth(1)} aria-label="Next month">
      <Icon name="chevron" size={14} />
    </button>
  </div>
  <div class="cal-dow">
    {#each WEEKDAYS as w (w)}
      <span>{w}</span>
    {/each}
  </div>
  <div class="cal-grid">
    {#each cells as cell (cell.iso)}
      <button
        type="button"
        class="cal-day {cell.inMonth ? '' : 'out'} {cell.iso === todayIso ? 'today' : ''}"
        class:sel={cell.iso === selected}
        aria-pressed={cell.iso === selected}
        aria-label={cell.iso}
        onclick={() => onPick?.(cell.iso)}
      >{cell.day}</button>
    {/each}
  </div>
</div>

<style>
  .calendar {
    width: 224px;
    user-select: none;
    -webkit-user-select: none;
  }
  .cal-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
  }
  .cal-title {
    font-size: 12px;
    font-weight: 600;
  }
  .cal-head .nav {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 2px 4px;
    border: none;
    background: transparent;
    color: var(--muted);
  }
  .cal-head .nav:hover {
    color: var(--accent-strong);
  }
  .cal-head .nav:first-child :global(.icon) {
    transform: rotate(180deg);
  }
  .cal-dow,
  .cal-grid {
    display: grid;
    grid-template-columns: repeat(7, 1fr);
    gap: 2px;
    text-align: center;
  }
  .cal-dow span {
    font-size: 11px;
    color: var(--muted);
    padding: 2px 0;
  }
  .cal-day {
    height: 26px;
    padding: 0;
    font-size: 12px;
    border: 1px solid transparent;
    background: transparent;
    border-radius: 8px;
    color: var(--text);
  }
  .cal-day.out {
    color: var(--muted);
    opacity: 0.55;
  }
  .cal-day.today {
    border-color: var(--line);
  }
  .cal-day:hover {
    border-color: var(--accent);
    background: var(--drop);
  }
  .cal-day.sel {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }
  .cal-day.sel:hover {
    background: var(--accent);
    color: white;
  }
</style>
