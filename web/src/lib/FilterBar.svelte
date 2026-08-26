<script>
  import { store } from './store.svelte.js';
  import { activeSorts } from './sorts.js';
  import Icon from './Icon.svelte';

</script>

<div class="filter">
  <div class="pill" class:invalid={store.filterError}>
    <Icon name="search" size={14} class="pill-icon" />
    <input
      type="text"
      class="search"
      placeholder="Filter: !has:complete label:work priority:high date:week …  (or just type to search)"
      value={store.filterText}
      oninput={(e) => store.setFilterText(e.target.value)}
    />
    {#if store.filterActive}
      <button class="ghost clear" title="Clear filter" onclick={() => store.clearFilter()}>
        <Icon name="close" size={14} />
      </button>
    {/if}
  </div>
  {#if activeSorts(store.filterText).length > 0}
    <div class="sort-chips" role="group" aria-label="Active sorts">
      {#each activeSorts(store.filterText) as s, i (s.raw + i)}
        <span class="chip" title={`${s.field} (${s.desc ? 'desc' : 'asc'})`}>
          <Icon name={s.field === 'priority' ? 'flag' : s.field === 'label' ? 'tag' : 'calendar'} size={11} />
          <span class="chip-label">{s.field}</span>
          <span class="chip-dir" aria-hidden="true">{s.desc ? '↓' : '↑'}</span>
        </span>
      {/each}
    </div>
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
  /* Same shape as the quick-add bar and todo cards; a raised background and
     inset icon keep it visually quieter than the row above. */
  .pill {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 0 8px;
    background: var(--raised);
    border: 1px solid var(--line);
    border-radius: 10px;
    transition:
      border-color 0.15s,
      box-shadow 0.15s;
  }
  .pill:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent-tint);
  }
  .pill.invalid {
    border-color: var(--danger);
  }
  .pill-icon {
    flex-shrink: 0;
    color: var(--muted);
  }
  .search {
    flex: 1;
    width: auto;
    border: none;
    background: transparent;
    padding: 8px 0;
    min-width: 0;
  }
  .search:focus {
    outline: none;
  }
  .clear {
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: 50%;
    color: var(--muted);
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
    border-radius: 8px;
    padding: 4px 8px;
    font-size: 12px;
    line-height: 1.4;
    z-index: 20;
  }

  /* Mobile: the search input keeps its clear button beside it; the sort
     chips drop to their own line below instead of squeezing the input. */
  @media (max-width: 720px) {
    .filter {
      flex-wrap: wrap;
    }
    .sort-chips {
      order: 3;
      flex-basis: 100%;
    }
  }
</style>
