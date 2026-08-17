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
  import SavedSearchMenu from './lib/SavedSearchMenu.svelte';
  import HelpModal from './lib/HelpModal.svelte';
  import ThemeToggle from './lib/ThemeToggle.svelte';
  import SyncStatus from './lib/SyncStatus.svelte';
  import MergeDialog from './lib/MergeDialog.svelte';

  const roots = $derived(store.visibleChildrenOf(null));

  let labelModalOpen = $state(false);
  let priorityModalOpen = $state(false);
  let helpModalOpen = $state(false);
  let boardModalOpen = $state(false);

  // Mobile hamburger: below 720px the toolbar collapses into a popout panel
  // under the header row; this flag is its open state.
  let menuOpen = $state(false);

  function toggleMenu() {
    menuOpen = !menuOpen;
  }

  function onWindowKeydown(e) {
    if (e.key === 'Escape' && menuOpen) menuOpen = false;
  }

  onMount(() => {
    store.readURL();
    store.load();
    store.watchEvents();
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

<svelte:window onkeydown={onWindowKeydown} />

<header>
  <div class="header-row">
    <img class="app-icon" src="/icon.svg" alt="" width="24" height="24" />
    <h1>Todo</h1>
    {#if store.loading}<span class="muted">Loading…</span>{/if}
    {#if store.error}<span class="error">{store.error}</span>{/if}
    <div class="header-side">
      <SyncStatus />
      <div class="toolbar" id="app-toolbar" class:open={menuOpen}>
        <ThemeToggle />
        <button
          class="ghost tool-btn"
          onclick={() => {
            menuOpen = false;
            boardModalOpen = true;
          }}
          title="Manage boards"
        >
          <Icon name="board" size={16} /><span class="tool-label">Boards</span>
        </button>
        <button
          class="ghost tool-btn"
          onclick={() => {
            menuOpen = false;
            labelModalOpen = true;
          }}
          title="Manage labels"
        >
          <Icon name="tag" size={16} /><span class="tool-label">Labels</span>
        </button>
        <button
          class="ghost tool-btn"
          onclick={() => {
            menuOpen = false;
            priorityModalOpen = true;
          }}
          title="Manage priorities"
        >
          <Icon name="flag" size={16} /><span class="tool-label">Priorities</span>
        </button>
        <SavedSearchMenu />
        <button
          class="ghost tool-btn"
          onclick={() => {
            menuOpen = false;
            helpModalOpen = true;
          }}
          title="Filter and date syntax help"
        >
          <Icon name="help" size={16} /><span class="tool-label">Help</span>
        </button>
      </div>
      {#if menuOpen}
        <button class="menu-backdrop" aria-label="Close menu" onclick={() => (menuOpen = false)}></button>
      {/if}
      <button
        class="ghost menu-btn"
        onclick={toggleMenu}
        aria-expanded={menuOpen}
        aria-controls="app-toolbar"
        aria-label={menuOpen ? 'Close menu' : 'Open menu'}
      >
        <Icon name={menuOpen ? 'close' : 'menu'} size={20} />
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
<!-- Rendered whenever the offline flush is paused on a clash; resolution
     unblocks the queue. -->
<MergeDialog />

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
    position: relative; /* anchors the mobile toolbar popout */
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
  .header-side {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .toolbar {
    display: inline-flex;
    gap: 4px;
  }
  .menu-btn {
    display: none; /* mobile-only; revealed by the media query below */
    align-items: center;
    justify-content: center;
    padding: 6px 8px;
  }
  .menu-backdrop {
    position: fixed;
    inset: 0;
    z-index: 100;
    background: transparent;
    border: none;
    padding: 0;
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

  /* Mobile: the toolbar becomes a popout panel toggled by the hamburger.
     The status badge stays in the header row so offline/sync state is
     visible without opening the menu. */
  @media (max-width: 720px) {
    header {
      padding: 10px 12px;
    }
    .header-row .muted,
    .header-row .error {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .menu-btn {
      display: inline-flex;
    }
    .toolbar {
      display: none;
    }
    .toolbar.open {
      display: flex;
      position: absolute;
      top: calc(100% + 8px);
      right: 0;
      flex-direction: column;
      align-items: stretch;
      gap: 2px;
      padding: 8px;
      min-width: 224px;
      background: var(--raised);
      border: 1px solid var(--line);
      border-radius: 10px;
      box-shadow: 0 8px 32px var(--shadow);
      z-index: 110;
    }
    /* Full-width entries for every tool button, including the ones rendered
       by child components (SavedSearchMenu) which need :global to reach. */
    .toolbar :global(button.tool-btn) {
      width: 100%;
      justify-content: flex-start;
      padding: 8px 10px;
    }
    .toolbar > :global(.theme-toggle) {
      align-self: flex-start;
      margin: 2px 0 6px 6px;
    }
    .list-toolbar {
      padding: 0 12px;
    }
    .empty-filter {
      padding: 24px 12px;
    }
    .no-boards {
      padding: 48px 12px;
    }
    .roots {
      padding: 12px 12px;
    }
  }
</style>
