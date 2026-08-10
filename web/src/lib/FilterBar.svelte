<script>
  import { store } from './store.svelte.js';
</script>

<div class="filter">
  <input
    type="text"
    class="search"
    placeholder="Filter: label:urgent date:week meeting…  (or just type to search)"
    value={store.filterText}
    oninput={(e) => store.setFilterText(e.target.value)}
  />
  {#if store.filterActive}
    <button class="ghost clear" onclick={() => store.clearFilter()}>✕</button>
  {/if}
</div>

<style>
  .filter {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .search {
    flex: 1;
    min-width: 0;
  }
  .clear {
    flex-shrink: 0;
  }
</style>
