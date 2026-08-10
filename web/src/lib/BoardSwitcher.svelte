<script>
  import { store } from './store.svelte.js';
  import { focus } from './actions.js';

  let adding = $state(false);
  let newName = $state('');
  let renamingId = $state(null);
  let renameDraft = $state('');
  let menuOpenFor = $state(null);

  const boards = $derived(store.boards);

  function startAdd() {
    newName = '';
    adding = true;
  }

  async function commitAdd() {
    const name = newName.trim();
    if (!name) {
      adding = false;
      return;
    }
    try {
      const created = await store.createBoard({ name });
      await store.selectBoard(created.id);
    } catch (e) {
      store.setError(e.message);
    }
    adding = false;
  }

  function startRename(b) {
    renameDraft = b.name;
    renamingId = b.id;
    menuOpenFor = null;
  }

  async function commitRename() {
    const id = renamingId;
    const name = renameDraft.trim();
    renamingId = null;
    if (!id) return;
    const cur = store.boardById(id);
    if (!cur || !name || name === cur.name) return;
    try {
      await store.renameBoard(id, name);
    } catch (e) {
      store.setError(e.message);
    }
  }

  async function onDelete(b) {
    menuOpenFor = null;
    if (!confirm(`Delete board "${b.name}" and all of its todos?`)) return;
    try {
      await store.deleteBoard(b.id);
    } catch (e) {
      store.setError(e.message);
    }
  }

  function toggleMenu(id) {
    menuOpenFor = menuOpenFor === id ? null : id;
  }
</script>

<nav class="boards" aria-label="Boards">
  {#each boards as b (b.id)}
    {#if renamingId === b.id}
      <form
        class="rename"
        onsubmit={(e) => {
          e.preventDefault();
          commitRename();
        }}
      >
        <input
          type="text"
          bind:value={renameDraft}
          use:focus
          onblur={commitRename}
        />
      </form>
    {:else}
      <button
        type="button"
        class="tab"
        class:active={b.id === store.activeBoardId}
        onclick={() => store.selectBoard(b.id)}
        ondblclick={() => startRename(b)}
        title={b.name}
      >
        <span class="tab-name">{b.name}</span>
      </button>
    {/if}
  {/each}

  {#if adding}
    <form
      class="rename"
      onsubmit={(e) => {
        e.preventDefault();
        commitAdd();
      }}
    >
      <input type="text" bind:value={newName} placeholder="Board name" use:focus />
    </form>
  {:else}
    <button type="button" class="tab add" onclick={startAdd} aria-label="Add board" title="Add board">
      ＋
    </button>
  {/if}

  {#if store.activeBoard}
    <div class="menu-wrap">
      <button
        type="button"
        class="tab menu-btn"
        onclick={() => toggleMenu(store.activeBoardId)}
        aria-label="Board options"
        title="Board options"
      >
        ⋯
      </button>
      {#if menuOpenFor === store.activeBoardId}
        <div class="menu" role="menu">
          <button type="button" role="menuitem" onclick={() => startRename(store.activeBoard)}>
            Rename
          </button>
          <button type="button" role="menuitem" class="danger" onclick={() => onDelete(store.activeBoard)}>
            Delete
          </button>
        </div>
      {/if}
    </div>
  {/if}
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
  .tab.add {
    font-size: 16px;
    padding: 4px 10px;
    line-height: 1;
  }
  .rename {
    display: inline-flex;
  }
  .rename input {
    width: 130px;
  }
  .menu-wrap {
    position: relative;
    margin-left: auto;
  }
  .menu-btn {
    font-size: 16px;
    line-height: 1;
  }
  .menu {
    position: absolute;
    right: 0;
    top: 100%;
    margin-top: 4px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    display: flex;
    flex-direction: column;
    min-width: 120px;
    z-index: 10;
  }
  .menu button {
    border: none;
    border-radius: 0;
    background: transparent;
    text-align: left;
    padding: 6px 10px;
  }
  .menu button:first-child {
    border-top-left-radius: 6px;
    border-top-right-radius: 6px;
  }
  .menu button:last-child {
    border-bottom-left-radius: 6px;
    border-bottom-right-radius: 6px;
  }
  .menu button:hover {
    background: var(--bg);
  }
  .menu .danger:hover {
    color: var(--danger);
  }
</style>
