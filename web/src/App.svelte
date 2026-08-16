<script>
  import { onMount } from 'svelte';
  import { store } from './lib/store.svelte.js';
  import TodoItem from './lib/TodoItem.svelte';
  import NewTodo from './lib/NewTodo.svelte';
  import BoardSwitcher from './lib/BoardSwitcher.svelte';
  import BoardModal from './lib/BoardModal.svelte';
  import FilterBar from './lib/FilterBar.svelte';
  import Icon from './lib/Icon.svelte';
  import LabelModal from './lib/LabelModal.svelte';
  import PriorityModal from './lib/PriorityModal.svelte';
  import HelpModal from './lib/HelpModal.svelte';
  import ThemeToggle from './lib/ThemeToggle.svelte';

  const roots = $derived(store.visibleChildrenOf(null));

  let labelModalOpen = $state(false);
  let priorityModalOpen = $state(false);
  let helpModalOpen = $state(false);
  let boardModalOpen = $state(false);

  onMount(() => {
    store.readURL();
    store.load();
    const onPop = () => {
      store.readURL();
      store.load();
    };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  });

  async function onAddRoot(payload) {
    await store.create(payload);
  }
</script>

<header>
  <div class="header-row">
    <img class="app-icon" src="/icon.svg" alt="" width="24" height="24" />
    <h1>Todo</h1>
    {#if store.loading}<span class="muted">Loading…</span>{/if}
    {#if store.error}<span class="error">{store.error}</span>{/if}
    <div class="toolbar">
      <ThemeToggle />
      <button class="ghost tool-btn" onclick={() => (boardModalOpen = true)} title="Manage boards">
        <Icon name="board" size={16} /><span class="tool-label">Boards</span>
      </button>
      <button class="ghost tool-btn" onclick={() => (labelModalOpen = true)} title="Manage labels">
        <Icon name="tag" size={16} /><span class="tool-label">Labels</span>
      </button>
      <button class="ghost tool-btn" onclick={() => (priorityModalOpen = true)} title="Manage priorities">
        <Icon name="flag" size={16} /><span class="tool-label">Priorities</span>
      </button>
      <button class="ghost tool-btn" onclick={() => (helpModalOpen = true)} title="Filter and date syntax help">
        <Icon name="help" size={16} /><span class="tool-label">Help</span>
      </button>
    </div>
  </div>
  {#if store.boards.length > 1}
    <BoardSwitcher />
  {/if}
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
{:else if !store.loading}
  <div class="no-boards">
    <p class="muted">No boards yet. Create one to start adding todos.</p>
    <button class="primary" onclick={() => (boardModalOpen = true)}>
      <Icon name="board" size={16} /> Create a board
    </button>
  </div>
{/if}

{#if boardModalOpen}
  <BoardModal onClose={() => (boardModalOpen = false)} />
{/if}
{#if labelModalOpen}
  <LabelModal onClose={() => (labelModalOpen = false)} />
{/if}
{#if priorityModalOpen}
  <PriorityModal onClose={() => (priorityModalOpen = false)} />
{/if}
{#if helpModalOpen}
  <HelpModal onClose={() => (helpModalOpen = false)} />
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
    align-items: center;
    gap: 12px;
  }
  h1 {
    font-size: 18px;
    margin: 0;
  }
  .app-icon {
    display: block;
    border-radius: 6px;
  }
  .muted {
    color: var(--muted);
  }
  .error {
    color: var(--danger);
  }
  .toolbar {
    margin-left: auto;
    display: inline-flex;
    gap: 4px;
  }
  .tool-btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    padding: 4px 8px;
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
  .no-boards {
    padding: 48px 20px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 12px;
  }
  .no-boards p {
    margin: 0;
    font-size: 13px;
  }
  .no-boards button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .roots {
    padding: 12px 20px;
    min-height: 60vh;
  }
  .roots > li {
    margin-bottom: 8px;
  }
</style>
