<script>
  import { store } from './store.svelte.js';
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  let { onClose } = $props();

  let newName = $state('');
  let renamingId = $state(null);
  let renameDraft = $state('');

  const boards = $derived(store.boards);

  async function addBoard() {
    const name = newName.trim();
    if (!name) return;
    try {
      await store.createBoard({ name });
      newName = '';
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  function startRename(b) {
    renameDraft = b.name;
    renamingId = b.id;
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
      alert(e.message || String(e));
    }
  }

  async function deleteBoard(b) {
    if (!confirm(`Delete board "${b.name}" and all of its todos?`)) return;
    try {
      await store.deleteBoard(b.id);
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  function onKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose?.();
    }
  }
</script>

<!-- Transparent full-screen layer captures outside clicks to dismiss. -->
<button class="backdrop" aria-label="Close" onclick={() => onClose?.()}></button>

<div class="modal" role="dialog" aria-label="Manage boards" tabindex="-1" use:focus onkeydown={onKeydown}>
  <div class="modal-header">
    <h2><Icon name="board" size={18} /> Boards</h2>
    <button class="ghost close-btn" onclick={() => onClose?.()} aria-label="Close">
      <Icon name="close" size={18} />
    </button>
  </div>

  <form class="add-form" onsubmit={(e) => { e.preventDefault(); addBoard(); }}>
    <input
      type="text"
      bind:value={newName}
      placeholder="New board name…"
      autocomplete="off"
      use:focus
    />
    <button type="submit" class="primary">Add</button>
  </form>

  <div class="hint">
    Boards are separate todo lists. The board switcher only appears when more than one exists.
  </div>

  {#if boards.length}
    <ul class="board-list">
      {#each boards as b (b.id)}
        <li class="board-row" class:active={b.id === store.activeBoardId}>
          {#if renamingId === b.id}
            <form
              class="rename-form"
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
              class="board-name-btn"
              onclick={() => store.selectBoard(b.id)}
              ondblclick={() => startRename(b)}
              title={b.name}
            >
              <span class="board-name">{b.name}</span>
              {#if b.id === store.activeBoardId}
                <span class="active-badge">Active</span>
              {/if}
            </button>
            <span class="board-actions">
              <button
                type="button"
                class="ghost"
                onclick={() => startRename(b)}
                aria-label="Rename board {b.name}"
                title="Rename"
              >
                <Icon name="edit" size={14} />
              </button>
              <button
                type="button"
                class="ghost danger"
                onclick={() => deleteBoard(b)}
                aria-label="Delete board {b.name}"
                title="Delete"
                disabled={boards.length <= 1}
              >
                <Icon name="trash" size={14} />
              </button>
            </span>
          {/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="empty">No boards yet. Add one above.</p>
  {/if}
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: var(--shadow);
    border: none;
    padding: 0;
    z-index: 100;
  }
  .modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    box-shadow: 0 8px 32px var(--shadow);
    padding: 16px 20px;
    z-index: 101;
    width: 440px;
    max-width: calc(100vw - 32px);
    max-height: calc(100vh - 64px);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .modal-header h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-size: 16px;
  }
  .close-btn {
    padding: 4px;
  }
  .add-form {
    display: flex;
    gap: 6px;
  }
  .hint {
    font-size: 12px;
    color: var(--muted);
    margin: 0;
  }
  .board-list {
    display: flex;
    flex-direction: column;
    gap: 0;
    max-height: 360px;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: 6px;
  }
  .board-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0;
    gap: 8px;
  }
  .board-row + .board-row {
    border-top: 1px solid var(--line);
  }
  .board-row.active {
    background: var(--bg);
  }
  .board-name-btn {
    flex: 1;
    display: inline-flex;
    align-items: center;
    gap: 8px;
    background: transparent;
    border: none;
    padding: 8px 10px;
    text-align: left;
    color: var(--text);
    min-width: 0;
  }
  .board-name-btn:hover {
    color: var(--accent);
  }
  .board-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .active-badge {
    font-size: 11px;
    color: var(--accent);
    border: 1px solid var(--accent);
    border-radius: 4px;
    padding: 0 4px;
    flex-shrink: 0;
  }
  .board-actions {
    display: inline-flex;
    gap: 2px;
    padding: 4px 8px 4px 0;
    flex-shrink: 0;
  }
  .rename-form {
    display: inline-flex;
    flex: 1;
    padding: 4px 8px;
  }
  .rename-form input {
    flex: 1;
  }
  .board-actions button {
    padding: 4px;
  }
  .empty {
    color: var(--muted);
    font-size: 13px;
    margin: 0;
  }
</style>
