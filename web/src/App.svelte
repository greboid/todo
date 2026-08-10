<script>
  import { onMount } from 'svelte';
  import { store } from './lib/store.svelte.js';
  import TodoItem from './lib/TodoItem.svelte';
  import NewTodo from './lib/NewTodo.svelte';
  import BoardSwitcher from './lib/BoardSwitcher.svelte';

  const roots = $derived(store.childrenOf(null));

  onMount(() => {
    store.load();
  });

  async function onAddRoot(title) {
    await store.create({ title });
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

  <ul
    class="roots"
    role="list"
    ondrop={(e) => {
      e.preventDefault();
      const id = Number(e.dataTransfer.getData('text/x-todo-id'));
      if (!id) return;
      store.move(id, { parentId: null, position: roots.length });
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
  .roots {
    padding: 12px 20px;
    min-height: 60vh;
  }
  .roots > li {
    margin-bottom: 8px;
  }
</style>
