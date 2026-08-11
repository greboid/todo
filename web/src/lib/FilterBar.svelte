<script>
  import { store } from './store.svelte.js';
  import Icon from './Icon.svelte';

  let helpOpen = $state(false);
  let sortOpen = $state(false);

  // Sort field metadata: label + the token value written into filterText.
  const SORT_FIELDS = [
    { id: 'priority', label: 'Priority', icon: 'flag' },
    { id: 'label', label: 'Label', icon: 'tag' },
    { id: 'date', label: 'Due date', icon: 'calendar' },
  ];

  // Parse sort: tokens out of the current filter text for display. Returns an
  // array of { field, desc, raw } in the order they appear — that order is the
  // tie-break priority (first wins, subsequent break ties).
  function activeSorts(text) {
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

  // Append a sort: token to the filter text. New sorts are added at the end so
  // they become the lowest-priority tiebreaker (existing sorts keep precedence).
  function addSort(field) {
    const text = store.filterText.trim();
    const token = `sort:${field}`;
    const next = text ? `${text} ${token}` : token;
    store.setFilterText(next);
  }

  // Toggle ascending/descending for the nth active sort by rewriting its token.
  function toggleSortDirection(index) {
    const sorts = activeSorts(store.filterText);
    if (index >= sorts.length) return;
    const target = sorts[index];
    const replacement = target.desc ? target.raw.replace(/^!/, '') : `!${target.raw}`;
    const next = store.filterText.replace(target.raw, replacement);
    store.setFilterText(next);
  }

  // Remove the nth active sort token from the filter text.
  function removeSort(index) {
    const sorts = activeSorts(store.filterText);
    if (index >= sorts.length) return;
    const target = sorts[index];
    // Replace the token and any single preceding space so we don't leave a
    // double space or a leading space behind.
    const next = store.filterText
      .replace(new RegExp(`\\s?${escapeRegex(target.raw)}`), '')
      .trim();
    store.setFilterText(next);
  }

  function escapeRegex(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
</script>

<div class="filter">
  <input
    type="text"
    class="search"
    class:invalid={store.filterError}
    placeholder="Filter: !has:complete label:work priority:high date:week …  (or just type to search)"
    value={store.filterText}
    oninput={(e) => store.setFilterText(e.target.value)}
  />
  {#if activeSorts(store.filterText).length > 0}
    <div class="sort-chips" role="group" aria-label="Active sorts">
      {#each activeSorts(store.filterText) as s, i (s.raw + i)}
        <button
          class="chip"
          title={`${s.field} — click to flip direction, × to remove`}
          onclick={() => toggleSortDirection(i)}
        >
          <Icon name={s.field === 'priority' ? 'flag' : s.field === 'label' ? 'tag' : 'calendar'} size={11} />
          <span class="chip-label">{s.field}</span>
          <span class="chip-dir" aria-hidden="true">{s.desc ? '↓' : '↑'}</span>
          <span
            class="chip-remove"
            role="button"
            tabindex="-1"
            aria-label="Remove sort"
            onclick={(e) => {
              e.stopPropagation();
              removeSort(i);
            }}
            onkeydown={(e) => e.key === 'Enter' && removeSort(i)}
          >
            <Icon name="close" size={10} />
          </span>
        </button>
      {/each}
    </div>
  {/if}
  {#if store.filterActive}
    <button class="ghost clear" title="Clear filter" onclick={() => store.clearFilter()}>
      <Icon name="close" size={14} />
    </button>
  {/if}
  <button
    class="ghost sort"
    class:open={sortOpen}
    title="Sort by priority, label, or due date"
    aria-expanded={sortOpen}
    onclick={() => (sortOpen = !sortOpen)}
  >
    <Icon name="chevron" size={14} />
  </button>
  <button
    class="ghost help"
    class:open={helpOpen}
    title="Filter syntax help"
    aria-expanded={helpOpen}
    onclick={() => (helpOpen = !helpOpen)}
  >
    <Icon name="ellipsis" size={14} />
  </button>
  {#if store.filterError}
    <span class="filter-error" role="alert">{store.filterError}</span>
  {/if}
  {#if sortOpen}
    <!-- Transparent full-screen layer captures outside clicks to dismiss. -->
    <button class="backdrop" aria-label="Close sort menu" onclick={() => (sortOpen = false)}></button>
    <div class="sort-popover" role="dialog" aria-label="Sort">
      <p class="popover-hint">Adds a <code>sort:</code> term. Repeat to break ties (order = priority).</p>
      <div class="sort-options">
        {#each SORT_FIELDS as f (f.id)}
          <button class="sort-option" onclick={() => addSort(f.id)}>
            <Icon name={f.icon} size={14} />
            <span>{f.label}</span>
          </button>
        {/each}
      </div>
    </div>
  {/if}
  {#if helpOpen}
    <!-- Transparent full-screen layer captures outside clicks to dismiss. -->
    <button class="backdrop" aria-label="Close filter help" onclick={() => (helpOpen = false)}></button>
    <div class="help-popover" role="dialog" aria-label="Filter syntax">
      <dl>
        <dt><code>label:<em>name</em></code></dt>
        <dd>has a label (OR when repeated)</dd>
        <dt><code>priority:<em>name</em></code></dt>
        <dd>has a priority (OR when repeated)</dd>
        <dt><code>date:<em>v</em></code></dt>
        <dd>
          due date: <code>week</code>, <code>overdue</code>, <code>none</code>, <code>today</code>,
          <code>tomorrow</code>, <code>YYYY-MM-DD</code>, or a <code>YYYY-MM-DD..YYYY-MM-DD</code> range.
          Combine presets with <code>or</code>/<code>and</code> in one value, e.g.
          <code>date:"overdue or today"</code>. Only one <code>date:</code> per query.
        </dd>
        <dt><code>has:<em>x</em></code></dt>
        <dd>existence: <code>complete</code>, <code>label</code>, <code>priority</code>, <code>recur</code>, <code>date</code></dd>
        <dt><code>sort:<em>field</em></code></dt>
        <dd>
          order siblings by <code>priority</code>, <code>label</code>, or <code>date</code>; repeat
          for tiebreakers (first wins). Prefix <code>!</code> to reverse, e.g. <code>sort:!priority</code>
        </dd>
        <dt><code>!<em>key</em>:<em>v</em></code></dt>
        <dd>negate <code>label</code>, <code>priority</code>, <code>date</code>, or <code>has</code></dd>
        <dt><em>text</em></dt>
        <dd>search title + description</dd>
        <dt><code>#<em>label</em></code> <code>@<em>priority</em></code></dt>
        <dd>quick-add tags in the new-todo field (e.g. <code>Buy milk #errands @high tomorrow</code>)</dd>
      </dl>
      <p class="hint">Default <code>!has:complete</code> hides completed. Invalid tokens show an error.</p>
    </div>
  {/if}
</div>

<style>
  .filter {
    display: flex;
    align-items: center;
    gap: 6px;
    position: relative;
  }
  .search {
    flex: 1;
    min-width: 0;
  }
  .search.invalid {
    outline: 2px solid var(--danger);
    outline-offset: -1px;
    border-color: var(--danger);
  }
  .clear,
  .help,
  .sort {
    flex-shrink: 0;
  }
  .sort :global(.icon) {
    transition: transform 0.12s ease;
  }
  .sort.open :global(.icon) {
    transform: rotate(90deg);
  }
  .sort-chips {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
    flex-wrap: wrap;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 4px 2px 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--raised);
    color: var(--text);
    font-size: 11px;
    line-height: 1.4;
    cursor: pointer;
    user-select: none;
  }
  .chip:hover {
    border-color: var(--accent, var(--text));
  }
  .chip-label {
    font-weight: 500;
  }
  .chip-dir {
    color: var(--muted);
    font-size: 10px;
  }
  .chip-remove {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    color: var(--muted);
    cursor: pointer;
  }
  .chip-remove:hover {
    background: var(--danger-tint);
    color: var(--danger);
  }
  .filter-error {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    margin-top: 4px;
    color: var(--danger);
    background: var(--danger-tint);
    border: 1px solid var(--danger);
    border-radius: 6px;
    padding: 4px 8px;
    font-size: 12px;
    line-height: 1.4;
    z-index: 20;
  }
  .backdrop {
    position: fixed;
    inset: 0;
    background: transparent;
    border: none;
    padding: 0;
    z-index: 15;
  }
  .help-popover {
    position: absolute;
    top: 100%;
    right: 0;
    margin-top: 4px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    padding: 10px 12px;
    z-index: 20;
    width: 320px;
    max-width: calc(100vw - 40px);
  }
  .help-popover dl {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .help-popover dt {
    color: var(--text);
    font-size: 12px;
  }
  .help-popover dd {
    margin: 0 0 0 12px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.4;
  }
  .help-popover code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 11px;
  }
  .hint {
    margin: 8px 0 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
  }
  .hint code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 10px;
  }
  .sort-popover {
    position: absolute;
    top: 100%;
    right: 28px;
    margin-top: 4px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    padding: 8px;
    z-index: 20;
    width: 200px;
    max-width: calc(100vw - 40px);
  }
  .sort-popover .popover-hint {
    margin: 0 0 6px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
  }
  .sort-popover .popover-hint code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 10px;
  }
  .sort-options {
    display: grid;
    gap: 2px;
  }
  .sort-option {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 6px 8px;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }
  .sort-option:hover {
    background: var(--raised);
  }
</style>
