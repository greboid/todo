<script>
  import { store, labelColor, LABEL_PALETTE } from './store.svelte.js';
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  let { onClose } = $props();

  let newName = $state('');
  let editingColor = $state(null);

  let orderedNames = $state([]);

  $effect(() => {
    orderedNames = store.priorities.filter((p) => p.position != null).map((p) => p.name);
  });

  let predefined = $derived(
    orderedNames
      .map((name) => store.priorities.find((p) => p.name === name))
      .filter(Boolean),
  );
  let adhoc = $derived(store.priorities.filter((p) => p.position == null));

  function moveItem(from, to) {
    if (from === to) return;
    const arr = [...orderedNames];
    const [item] = arr.splice(from, 1);
    arr.splice(to, 0, item);
    orderedNames = arr;
  }

  async function saveOrder() {
    try {
      await store.reorderPriorities(orderedNames);
      onClose?.();
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  async function addPredefined() {
    const name = newName.trim();
    if (!name) return;
    try {
      await store.addPredefinedPriority(name);
      newName = '';
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  async function removePredefined(name) {
    try {
      await store.removePredefinedPriority(name);
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  async function setPriorityColor(name, color) {
    try {
      await store.updatePriorityColor(name, color);
    } catch (e) {
      alert(e.message || String(e));
    }
  }

  function moveUp(index) {
    if (index === 0) return;
    moveItem(index, index - 1);
  }

  function moveDown(index) {
    if (index === orderedNames.length - 1) return;
    moveItem(index, index + 1);
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

<div class="modal" role="dialog" aria-label="Manage priorities" tabindex="-1" use:focus onkeydown={onKeydown}>
  <div class="modal-header">
    <h2><Icon name="flag" size={18} /> Priorities</h2>
    <button class="ghost close-btn" onclick={() => onClose?.()} aria-label="Close">
      <Icon name="close" size={18} />
    </button>
  </div>

  <form class="add-form" onsubmit={(e) => { e.preventDefault(); addPredefined(); }}>
    <input
      type="text"
      bind:value={newName}
      placeholder="New priority name…"
      autocomplete="off"
      use:focus
    />
    <button type="submit" class="primary">Add</button>
  </form>

  <div class="hint">
    Use the arrows to reorder. The order controls <code>sort:priority</code>. Type <code>@name</code> when adding a todo to set a priority.
  </div>

  {#if predefined.length}
    <ul class="priority-list">
      {#each predefined as p, i (p.name)}
        <li class="priority-row">
          <span class="priority-index">{i + 1}</span>
          <span class="priority-name">
            <span class="color-dot" style:background={labelColor(p.name, p.color)}></span>
            {p.name}
          </span>
          <span class="priority-actions">
            <button type="button" class="ghost arrow-btn" onclick={() => moveUp(i)} disabled={i === 0} aria-label="Move up">▲</button>
            <button type="button" class="ghost arrow-btn" onclick={() => moveDown(i)} disabled={i === predefined.length - 1} aria-label="Move down">▼</button>
            <button
              type="button"
              class="ghost color-btn"
              style:background={labelColor(p.name, p.color)}
              onclick={() => (editingColor = editingColor === p.name ? null : p.name)}
              aria-label="Set colour for {p.name}"
            ></button>
            <button
              type="button"
              class="ghost danger remove-btn"
              onclick={() => removePredefined(p.name)}
              aria-label="Remove priority {p.name}"
              title="Remove from predefined set"
            >
              <Icon name="trash" size={14} />
            </button>
          </span>
          {#if editingColor === p.name}
            <div class="color-picker">
              <button
                type="button"
                class="color-swatch auto"
                title="Auto"
                onclick={() => { setPriorityColor(p.name, ''); editingColor = null; }}
              >Auto</button>
              {#each LABEL_PALETTE as c (c)}
                <button
                  type="button"
                  class="color-swatch"
                  class:selected={p.color === c}
                  style:background={c}
                  onclick={() => { setPriorityColor(p.name, c); editingColor = null; }}
                  aria-label="Set colour to {c}"
                ></button>
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
    <button type="button" class="primary save-order" onclick={saveOrder}>Save order</button>
  {:else}
    <p class="empty">No predefined priorities. Add one above.</p>
  {/if}

  {#if adhoc.length}
    <div class="adhoc-section">
      <div class="adhoc-title">In use (not in the ordered list)</div>
      <ul class="adhoc-list">
        {#each adhoc as p (p.name)}
          <li class="adhoc-row">
            <span class="priority-name">
              <span class="color-dot" style:background={labelColor(p.name, p.color)}></span>
              {p.name}
            </span>
            <span class="priority-actions">
              <button
                type="button"
                class="ghost color-btn"
                style:background={labelColor(p.name, p.color)}
                onclick={() => (editingColor = editingColor === p.name ? null : p.name)}
                aria-label="Set colour for {p.name}"
              ></button>
              <button
                type="button"
                class="ghost danger remove-btn"
                onclick={() => removePredefined(p.name)}
                aria-label="Remove priority {p.name}"
              >
                <Icon name="trash" size={14} />
              </button>
            </span>
            {#if editingColor === p.name}
              <div class="color-picker">
                <button
                  type="button"
                  class="color-swatch auto"
                  title="Auto"
                  onclick={() => { setPriorityColor(p.name, ''); editingColor = null; }}
                >Auto</button>
                {#each LABEL_PALETTE as c (c)}
                  <button
                    type="button"
                    class="color-swatch"
                    class:selected={p.color === c}
                    style:background={c}
                    onclick={() => { setPriorityColor(p.name, c); editingColor = null; }}
                    aria-label="Set colour to {c}"
                  ></button>
                {/each}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    </div>
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
    width: 480px;
    max-width: calc(100vw - 32px);
    max-height: calc(100vh - 64px);
    overflow-y: auto;
    overscroll-behavior: contain;
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
  .hint code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
  }
  .priority-list {
    display: flex;
    flex-direction: column;
    gap: 0;
    border: 1px solid var(--line);
    border-radius: 8px;
  }
  .priority-row {
    display: flex;
    align-items: center;
    padding: 6px 8px;
    gap: 8px;
    flex-wrap: wrap;
    transition: background 0.1s;
  }
  .priority-row + .priority-row {
    border-top: 1px solid var(--line);
  }
  .priority-index {
    font-size: 12px;
    color: var(--muted);
    min-width: 16px;
    text-align: center;
    flex-shrink: 0;
  }
  .priority-name {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    flex: 1;
  }
  .color-dot {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .priority-actions {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .arrow-btn {
    padding: 2px 4px;
    font-size: 10px;
    line-height: 1;
  }
  .arrow-btn:disabled {
    opacity: 0.3;
    cursor: default;
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
  .save-order {
    align-self: flex-start;
  }
  .empty {
    color: var(--muted);
    font-size: 13px;
    text-align: center;
    padding: 24px 0;
  }
  .adhoc-section {
    border-top: 1px solid var(--line);
    padding-top: 8px;
  }
  .adhoc-title {
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 6px;
  }
  .adhoc-list {
    border: 1px solid var(--line);
    border-radius: 8px;
  }
  .adhoc-row {
    display: flex;
    align-items: center;
    padding: 6px 8px;
    gap: 8px;
    flex-wrap: wrap;
  }
  .adhoc-row + .adhoc-row {
    border-top: 1px solid var(--line);
  }
</style>
