<script>
  import { store } from './store.svelte.js';
  import Icon from './Icon.svelte';

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
      {#each activeSorts(store.filterText) as s (s.raw + i)}
        <span class="chip" title={`${s.field} (${s.desc ? 'desc' : 'asc'})`}>
          <Icon name={s.field === 'priority' ? 'flag' : s.field === 'label' ? 'tag' : 'calendar'} size={11} />
          <span class="chip-label">{s.field}</span>
          <span class="chip-dir" aria-hidden="true">{s.desc ? '↓' : '↑'}</span>
        </span>
      {/each}
    </div>
  {/if}
  {#if store.filterActive}
    <button class="ghost clear action-btn" title="Clear filter" onclick={() => store.clearFilter()}>
      <Icon name="close" size={14} />
    </button>
  {:else}
    <span class="action-btn placeholder" aria-hidden="true"></span>
  {/if}
  {#if store.filterError}
    <span class="filter-error" role="alert">{store.filterError}</span>
  {/if}
</div>

<style>
  .filter {
    display: flex;
    align-items: center;
    gap: 8px;
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
  .clear {
    flex-shrink: 0;
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
    padding: 2px 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--raised);
    color: var(--text);
    font-size: 11px;
    line-height: 1.4;
    user-select: none;
  }
  .chip-label {
    font-weight: 500;
  }
  .chip-dir {
    color: var(--muted);
    font-size: 10px;
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
</style>
