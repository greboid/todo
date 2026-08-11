<script>
  import { store } from './store.svelte.js';
</script>

<nav class="boards" aria-label="Boards">
  {#each store.boards as b (b.id)}
    <button
      type="button"
      class="tab"
      class:active={b.id === store.activeBoardId}
      onclick={() => store.selectBoard(b.id)}
      title={b.name}
    >
      <span class="tab-name">{b.name}</span>
    </button>
  {/each}
</nav>

<style>
  .boards {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
  }
  .tab {
    background: transparent;
    border: 1px solid transparent;
    border-radius: 6px;
    padding: 4px 10px;
    color: var(--muted);
    max-width: 160px;
    overflow: hidden;
  }
  .tab:hover {
    border-color: var(--line);
    color: var(--text);
  }
  .tab.active {
    background: var(--accent);
    border-color: var(--accent);
    color: white;
  }
  .tab-name {
    display: inline-block;
    max-width: 140px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
  }
</style>
