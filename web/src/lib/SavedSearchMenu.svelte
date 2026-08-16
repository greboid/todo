<script>
  import { store } from './store.svelte.js';
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  // Toolbar dropdown for saved searches: apply one to the filter, save the
  // current filter under a name, or delete a saved search. The panel mirrors
  // the modal pattern (transparent backdrop closes on outside click).
  let open = $state(false);
  let newName = $state('');
  let error = $state('');

  function toggle() {
    open = !open;
    if (!open) {
      newName = '';
      error = '';
    }
  }

  function close() {
    open = false;
    newName = '';
    error = '';
  }

  function apply(id) {
    store.applySavedSearch(id);
    close();
  }

  async function save() {
    const name = newName.trim();
    if (!name) return;
    error = '';
    try {
      await store.createSavedSearch(name, store.filterText);
      newName = '';
    } catch (e) {
      error = e.message || String(e);
    }
  }

  async function remove(id) {
    error = '';
    try {
      await store.deleteSavedSearch(id);
    } catch (e) {
      error = e.message || String(e);
    }
  }

  function onKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }
</script>

<div class="saved-searches">
  <button
    type="button"
    class="ghost tool-btn"
    class:active={open}
    onclick={toggle}
    title="Saved searches"
    aria-haspopup="menu"
    aria-expanded={open}
  >
    <Icon name="search" size={16} /><span class="tool-label">Searches</span>
  </button>

  {#if open}
    <!-- Transparent layer captures outside clicks to dismiss. -->
    <button class="backdrop" aria-label="Close" onclick={close}></button>

    <div class="menu" role="menu" aria-label="Saved searches" tabindex="-1" use:focus onkeydown={onKeydown}>
      {#if store.savedSearches.length}
        <ul class="search-list">
          {#each store.savedSearches as s (s.id)}
            <li class="search-row">
              <button
                type="button"
                class="ghost search-btn"
                role="menuitem"
                onclick={() => apply(s.id)}
                title={s.query}
              >
                <span class="search-name">{s.name}</span>
                <span class="search-query">{s.query}</span>
              </button>
              <button
                type="button"
                class="ghost danger remove-btn"
                onclick={() => remove(s.id)}
                aria-label="Delete saved search {s.name}"
                title="Delete saved search"
              >
                <Icon name="trash" size={14} />
              </button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="empty">No saved searches yet.</p>
      {/if}

      <form class="save-form" onsubmit={(e) => { e.preventDefault(); save(); }}>
        <input
          type="text"
          bind:value={newName}
          placeholder={store.filterActive ? 'Name this search…' : 'Enter a filter first…'}
          autocomplete="off"
          disabled={!store.filterActive}
        />
        <button type="submit" class="primary" disabled={!store.filterActive || !newName.trim()}>Save</button>
      </form>
      <div class="hint">Saving stores the current filter.</div>
      {#if error}
        <div class="save-error" role="alert">{error}</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .saved-searches {
    position: relative;
    display: inline-flex;
  }
  .tool-btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    padding: 4px 8px;
  }
  .tool-btn.active {
    color: var(--text);
  }
  .backdrop {
    position: fixed;
    inset: 0;
    background: transparent;
    border: none;
    padding: 0;
    z-index: 100;
  }
  .menu {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    width: 280px;
    max-width: calc(100vw - 32px);
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    box-shadow: 0 8px 32px var(--shadow);
    padding: 10px;
    z-index: 101;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .search-list {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--line);
    border-radius: 6px;
    overflow: hidden;
  }
  .search-row {
    display: flex;
    align-items: stretch;
  }
  .search-row + .search-row {
    border-top: 1px solid var(--line);
  }
  .search-btn {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 1px;
    padding: 6px 8px;
    border-radius: 0;
    text-align: left;
  }
  .search-btn:hover {
    border-color: transparent;
    background: var(--raised);
  }
  .search-name {
    font-size: 13px;
    color: var(--text);
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .search-query {
    font-size: 11px;
    color: var(--muted);
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .remove-btn {
    display: inline-flex;
    align-items: center;
    padding: 0 8px;
    border-left: 1px solid var(--line);
    border-radius: 0;
  }
  .empty {
    color: var(--muted);
    font-size: 13px;
    text-align: center;
    margin: 8px 0;
  }
  .save-form {
    display: flex;
    gap: 6px;
  }
  .save-form input {
    min-width: 0;
    flex: 1;
  }
  .save-form input:disabled {
    opacity: 0.6;
  }
  .hint {
    font-size: 11px;
    color: var(--muted);
  }
  .save-error {
    color: var(--danger);
    background: var(--danger-tint);
    border: 1px solid var(--danger);
    border-radius: 6px;
    padding: 4px 8px;
    font-size: 12px;
    line-height: 1.4;
  }
</style>
