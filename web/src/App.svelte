<script>
  import { onMount } from 'svelte';
  import { store } from './lib/store.svelte.js';
  import TodoItem from './lib/TodoItem.svelte';
  import NewTodo from './lib/NewTodo.svelte';
  import BoardSwitcher from './lib/BoardSwitcher.svelte';
  import FilterBar from './lib/FilterBar.svelte';

  const roots = $derived(store.visibleChildrenOf(null));

  onMount(() => {
    store.load();
  });

  async function onAddRoot(payload) {
    await store.create(payload);
  }
</script>

<header>
  <div class="header-row">
    <h1>Todo</h1>
    {#if store.loading}<span class="muted">Loading…</span>{/if}
    {#if store.error}<span class="error">{store.error}</span>{/if}
  </div>
  <BoardSwitcher />
</header>

{#if store.activeBoard}
  <NewTodo placeholder="Add a top-level todo…" onAdd={onAddRoot} />

  <div class="list-toolbar">
    <FilterBar />
  </div>

  {#if store.filterActive && roots.length === 0}
    <div class="empty-filter">
      <span class="muted">No todos match the current filter.</span>
      <button class="ghost" onclick={() => store.clearFilter()}>Show all</button>
    </div>
  {/if}

  <ul
    class="roots"
    role="list"
    ondrop={(e) => {
      e.preventDefault();
      const id = Number(e.dataTransfer.getData('text/x-todo-id'));
      // No position => server appends at the end of the root siblings.
      if (id) store.move(id, { parentId: null });
    }}
    ondragover={(e) => {
      // Allow drops; if the pointer is over a child TodoItem it will stop
      // propagation and handle its own drop with a precise index.
      e.preventDefault();
    }}
  >
    {#each roots as todo (todo.id)}
      <li>
        <TodoItem {todo} />
      </li>
    {/each}
  </ul>
{/if}

<style>
  header {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 20px;
    border-bottom: 1px solid var(--line);
    background: var(--panel);
  }
  .header-row {
    display: flex;
    align-items: baseline;
    gap: 12px;
  }
  h1 {
    font-size: 18px;
    margin: 0;
  }
  .muted {
    color: var(--muted);
  }
  .error {
    color: var(--danger);
  }
  .list-toolbar {
    padding: 0 20px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .empty-filter {
    padding: 24px 20px;
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--muted);
    font-size: 13px;
  }
  .roots {
    padding: 12px 20px;
    min-height: 60vh;
  }
  .roots > li {
    margin-bottom: 8px;
  }
</style>
