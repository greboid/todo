<script>
  import { store, labelColor, LABEL_PALETTE } from './store.svelte.js';
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  let { onClose } = $props();

  let newName = $state('');
  let editingColor = $state(null);

  async function addPredefined() {
    const name = newName.trim();
    if (!name) return;
    try {
      await store.addPredefinedLabel(name);
      newName = '';
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  async function removePredefined(name) {
    try {
      await store.removePredefinedLabel(name);
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  async function setLabelColor(name, color) {
    try {
      await store.updateLabelColor(name, color);
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

<div class="modal" role="dialog" aria-label="Manage labels" tabindex="-1" use:focus onkeydown={onKeydown}>
  <div class="modal-header">
    <h2><Icon name="tag" size={18} /> Labels</h2>
    <button class="ghost close-btn" onclick={() => onClose?.()} aria-label="Close">
      <Icon name="close" size={18} />
    </button>
  </div>

  <form class="add-form" onsubmit={(e) => { e.preventDefault(); addPredefined(); }}>
    <input
      type="text"
      bind:value={newName}
      placeholder="New label name…"
      autocomplete="off"
      use:focus
    />
    <button type="submit" class="primary">Add</button>
  </form>

  <div class="hint">
    Labels are case-insensitive. Colours are shared across all boards.
  </div>

  {#if store.labels.length}
    <ul class="label-list">
      {#each store.labels as lbl (lbl.name)}
        <li class="label-row">
          <span class="label-name">
            <span class="color-dot" style:background={labelColor(lbl.name, lbl.color)}></span>
            {lbl.name}
          </span>
          <span class="label-actions">
            <button
              type="button"
              class="ghost color-btn"
              style:background={labelColor(lbl.name, lbl.color)}
              onclick={() => (editingColor = editingColor === lbl.name ? null : lbl.name)}
              aria-label="Set colour for {lbl.name}"
            ></button>
            <button
              type="button"
              class="ghost danger remove-btn"
              onclick={() => removePredefined(lbl.name)}
              aria-label="Remove label {lbl.name}"
              title="Remove from predefined set"
            >
              <Icon name="trash" size={14} />
            </button>
          </span>
          {#if editingColor === lbl.name}
            <div class="color-picker">
              <button
                type="button"
                class="color-swatch auto"
                title="Auto"
                onclick={() => { setLabelColor(lbl.name, ''); editingColor = null; }}
              >Auto</button>
              {#each LABEL_PALETTE as c (c)}
                <button
                  type="button"
                  class="color-swatch"
                  class:selected={lbl.color === c}
                  style:background={c}
                  onclick={() => { setLabelColor(lbl.name, c); editingColor = null; }}
                  aria-label="Set colour to {c}"
                ></button>
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="empty">No labels yet. Add one above or type <code>#name</code> when creating a todo.</p>
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
  .label-list {
    display: flex;
    flex-direction: column;
    gap: 0;
    max-height: 360px;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: 6px;
  }
  .label-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 8px;
    gap: 8px;
    flex-wrap: wrap;
  }
  .label-row + .label-row {
    border-top: 1px solid var(--line);
  }
  .label-name {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
  }
  .color-dot {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .label-actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .color-btn {
    width: 20px;
    height: 20px;
    padding: 0;
    border-radius: 50%;
    border: 2px solid var(--line);
    cursor: pointer;
  }
  .color-btn:hover {
    border-color: var(--text);
  }
  .remove-btn {
    padding: 4px;
  }
  .color-picker {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
    padding: 6px 8px;
    width: 100%;
    border-top: 1px dashed var(--line);
  }
  .color-swatch {
    width: 20px;
    height: 20px;
    padding: 0;
    border-radius: 50%;
    border: 2px solid transparent;
    cursor: pointer;
  }
  .color-swatch.selected {
    border-color: var(--text);
  }
  .color-swatch.auto {
    width: auto;
    padding: 0 8px;
    border: 1px solid var(--line);
    background: transparent;
    color: var(--muted);
    font-size: 10px;
    border-radius: 999px;
    height: 20px;
  }
  .empty {
    color: var(--muted);
    font-size: 13px;
    text-align: center;
    padding: 24px 0;
  }
  .empty code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
  }
</style>
