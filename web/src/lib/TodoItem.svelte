<script>
  import { store } from './store.svelte.js';
  import NewTodo from './NewTodo.svelte';
  import Icon from './Icon.svelte';
  import Self from './TodoItem.svelte';
  import { focus } from './actions.js';
  import { api } from './api.js';

  let { todo } = $props();
  // editing is driven by the global single-edit slot in the store.
  let editing = $derived(store.editingId === todo.id);
  let draftTitle = $state('');
  let draftDesc = $state('');
  let draftLabels = $state('');
  let draftSchedule = $state(''); // combined free-text due+recurrence field
  let addingChild = $state(false);
  // Single-click on the row toggles a detail view (the description). A short
  // debounce lets a following double-click (which edits) cancel the toggle,
  // so single-click expand and double-click edit coexist without flicker.
  let expanded = $state(false);
  let clickTimer = null;

  // Inline label picker state.
  let labelPickerOpen = $state(false);
  let labelQuery = $state('');
  let activeLabels = $state([]);
  let managePredefinedOpen = $state(false);
  let predefinedQuery = $state('');

  // Tracks where a dragged item would land if dropped here ("before", "after",
  // "into"). Used to render an insertion indicator.
  let dropZone = $state(null);
  let dragOverSelf = $state(false);

  // Shake animation: plays on this item when it refuses to yield its edit slot.
  let lastRejection = $state(0);
  let rootEl;
  $effect(() => {
    const tick = store.rejectionTick;
    if (tick !== lastRejection) {
      lastRejection = tick;
      if (editing && rootEl) {
        rootEl.animate(
          [
            { transform: 'translateX(0)' },
            { transform: 'translateX(-6px)' },
            { transform: 'translateX(6px)' },
            { transform: 'translateX(-4px)' },
            { transform: 'translateX(4px)' },
            { transform: 'translateX(0)' },
          ],
          { duration: 320, easing: 'ease-in-out' },
        );
      }
    }
  });

  const children = $derived(store.visibleChildrenOf(todo.id));

  // Existing labels that match the current typeahead query and aren't applied.
  const labelSuggestions = $derived(
    store.labels
      .filter((l) => !activeLabels.includes(l) && l.toLowerCase().includes(labelQuery.toLowerCase()))
      .slice(0, 8),
  );

  function onHeadClick(e) {
    // Nothing to reveal when there's no description; double-click still edits.
    if (!todo.description) return;
    // Ignore clicks on the checkbox, buttons, or the action cluster.
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'BUTTON' || tag === 'TEXTAREA' || e.target.closest('.actions')) return;
    // A rapid second click is the start of a double-click — cancel the toggle.
    if (clickTimer) {
      clearTimeout(clickTimer);
      clickTimer = null;
      return;
    }
    clickTimer = setTimeout(() => {
      clickTimer = null;
      expanded = !expanded;
    }, 220);
  }

  function onHeadDblClick(e) {
    if (clickTimer) {
      clearTimeout(clickTimer);
      clickTimer = null;
    }
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'BUTTON' || e.target.closest('.actions')) return;
    startEdit();
  }

  function startEdit() {
    if (editing) return;
    // Seed drafts first so they're ready when beginEdit flips editingId.
    draftTitle = todo.title;
    draftDesc = todo.description || '';
    draftLabels = (todo.labels || []).join(', ');
    draftSchedule = todo.scheduleText || '';
    // beginEdit refuses if another todo holds a dirty edit. On refusal this
    // todo stays in view mode; the store bumped rejectionTick so the other
    // item can shake.
    if (!store.beginEdit(todo.id)) {
      return;
    }
  }

  function cancelEdit() {
    store.endEdit();
  }

  async function saveEdit() {
    const labels = draftLabels
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    const patch = {
      title: draftTitle.trim() || todo.title,
      description: draftDesc,
      labels,
    };
    // One field drives both due date and recurrence. Empty clears both. The
    // grammar lives server-side now, so parsing goes through the API.
    let parsed;
    try {
      parsed = await api.parseSchedule(draftSchedule);
    } catch (e) {
      parsed = { ok: false, error: e.message };
    }
    if (!parsed.ok) {
      return; // live feedback shows the parse error; keep the edit open
    }
    patch.dueDate = parsed.dueDate;
    patch.recurrence = parsed.recurrence;
    await store.update(todo.id, patch);
    store.endEdit();
  }

  // ISO date (YYYY-MM-DD) in the user's local timezone, for overdue checks.
  function todayISO() {
    const d = new Date();
    const mm = String(d.getMonth() + 1).padStart(2, '0');
    const dd = String(d.getDate()).padStart(2, '0');
    return `${d.getFullYear()}-${mm}-${dd}`;
  }

  // Live parse feedback for the schedule field: shows the canonical form as
  // the user types, or a parse error. The grammar lives server-side now, so
  // parsing is debounced through the API; feedback is null while a parse is
  // pending or the field is empty. parseSeq discards stale responses.
  let parseResult = $state(null);
  let parseSeq = 0;
  $effect(() => {
    const text = draftSchedule;
    if (!text.trim()) {
      parseResult = null;
      return;
    }
    const seq = ++parseSeq;
    const handle = setTimeout(async () => {
      const res = await api.parseSchedule(text).catch((e) => ({ ok: false, error: e.message }));
      if (seq === parseSeq) parseResult = { ...res, src: text };
    }, 250);
    return () => clearTimeout(handle);
  });
  let scheduleFeedback = $derived.by(() => {
    if (!draftSchedule.trim() || !parseResult || parseResult.src !== draftSchedule) return null;
    return parseResult.ok
      ? { ok: true, text: parseResult.scheduleText }
      : { ok: false, text: parseResult.error };
  });

  async function toggleCompleted() {
    await store.setCompleted(todo.id, !todo.completed);
  }
  async function onDelete() {
    if (!confirm(`Delete "${todo.title}"?`)) return;
    await store.remove(todo.id);
  }

  async function addChild(title) {
    await store.create({ title, parentId: todo.id });
  }

  function openLabelPicker() {
    activeLabels = [...(todo.labels || [])];
    labelQuery = '';
    labelPickerOpen = true;
  }

  function closeLabelPicker() {
    labelPickerOpen = false;
  }

  function onLabelPickerKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeLabelPicker();
    }
  }

  function onEditKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      cancelEdit();
    }
  }

  function toggleExisting(label) {
    if (activeLabels.includes(label)) {
      activeLabels = activeLabels.filter((l) => l !== label);
    } else {
      activeLabels = [...activeLabels, label];
    }
  }

  function commitLabelQuery() {
    const name = labelQuery.trim();
    if (!name) return;
    if (!activeLabels.includes(name)) activeLabels = [...activeLabels, name];
    labelQuery = '';
  }

  async function saveLabels() {
    await store.update(todo.id, { labels: [...activeLabels] });
    labelPickerOpen = false;
  }

  async function addPredefined() {
    const name = predefinedQuery.trim();
    if (!name) return;
    try {
      await store.addPredefinedLabel(name);
      predefinedQuery = '';
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

  function onDragStart(e) {
    // A child todo's dragstart would bubble up through every ancestor .item and
    // re-fire this handler with the ancestor's `todo`, overwriting the dragged
    // id. Stop propagation so only the dragged element's own handler runs.
    e.stopPropagation();
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/x-todo-id', String(todo.id));
    // Stop the row from being treated as its own drop target while dragging.
    dragOverSelf = true;
  }

  function onDragEnd() {
    dragOverSelf = false;
    dropZone = null;
  }

  // Decide whether to drop before, after, or nested based on pointer position.
  function zoneFromEvent(e) {
    const rect = e.currentTarget.getBoundingClientRect();
    const y = e.clientY - rect.top;
    const ratio = y / rect.height;
    if (ratio < 0.3) return 'before';
    if (ratio > 0.7) return 'after';
    return 'into';
  }

  function resolveDrop(e) {
    e.preventDefault();
    e.stopPropagation();
    const id = Number(e.dataTransfer.getData('text/x-todo-id'));
    if (!id || id === todo.id) return;
    const zone = zoneFromEvent(e);
    if (zone === 'into') {
      store.move(id, { parentId: todo.id, position: store.childrenOf(todo.id).length });
    } else {
      const siblings = store.childrenOf(todo.parentId ?? null);
      const myIndex = siblings.findIndex((t) => t.id === todo.id);
      // Moving before/after a sibling uses absolute target index.
      const targetIndex = zone === 'before' ? myIndex : myIndex + 1;
      // Root todos serialize with omitempty parentId, so todo.parentId is
      // undefined at runtime — coerce to null so the store treats this as an
      // explicit move-to-root rather than "leave parent unchanged".
      store.move(id, { parentId: todo.parentId ?? null, position: targetIndex });
    }
    dropZone = null;
  }

  function onDragOver(e) {
    const id = Number(e.dataTransfer.getData('text/x-todo-id'));
    // dataTransfer is empty during dragover in some browsers; just allow.
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    dropZone = zoneFromEvent(e);
  }

  function onDragLeave(e) {
    // Only clear when leaving the element entirely (relatedTarget is outside).
    if (!e.currentTarget.contains(e.relatedTarget)) {
      dropZone = null;
    }
  }

  function onChildListDrop(e) {
    // Dropping onto the empty space at the bottom of this todo's children.
    e.preventDefault();
    e.stopPropagation();
    const id = Number(e.dataTransfer.getData('text/x-todo-id'));
    if (!id || id === todo.id) return;
    store.move(id, { parentId: todo.id, position: store.childrenOf(todo.id).length });
  }

  let classes = $derived(
    `item ${todo.completed ? 'done' : ''} ${dropZone ? `drop drop-${dropZone}` : ''} ${expanded ? 'expanded' : ''}`,
  );
</script>

<div
  bind:this={rootEl}
  class={classes}
  draggable={!editing}
  role="treeitem"
  tabindex="0"
  aria-selected={todo.completed}
  aria-expanded={children.length > 0}
  ondragstart={onDragStart}
  ondragend={onDragEnd}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={resolveDrop}
>
  {#if editing}
    <form class="edit" onsubmit={(e) => { e.preventDefault(); saveEdit(); }}>
      <input type="text" bind:value={draftTitle} oninput={store.markEditDirty} onkeydown={onEditKeydown} use:focus />
      <textarea bind:value={draftDesc} rows="2" placeholder="Description" oninput={store.markEditDirty}></textarea>
      <input type="text" bind:value={draftLabels} placeholder="Labels (comma separated)" oninput={store.markEditDirty} />
      <input
        type="text"
        class="schedule"
        bind:value={draftSchedule}
        placeholder="Due date or repeat, e.g. aug 15, every 2 weeks on mon, wed, every month on the 15th starting sep 1"
        oninput={store.markEditDirty}
      />
      {#if scheduleFeedback}
        <span class="rc-feedback {scheduleFeedback.ok ? 'ok' : 'err'}">
          {scheduleFeedback.ok ? `→ ${scheduleFeedback.text}` : `✗ ${scheduleFeedback.text}`}
        </span>
      {/if}
      <div class="row">
        <button type="submit" class="primary">Save</button>
        <button type="button" onclick={() => store.endEdit()}>Cancel</button>
      </div>
    </form>
  {:else}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      class="head"
      role="group"
      onclick={onHeadClick}
      ondblclick={onHeadDblClick}
    >
      <span class="disclosure">{#if todo.description}<Icon name="chevron" size={14} />{/if}</span>
      <input type="checkbox" checked={todo.completed} onchange={toggleCompleted} />
      <span
        class="title"
        role="button"
        tabindex="0"
        onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); startEdit(); } }}
      >{todo.title}</span>
      {#if todo.labels?.length}
        <span class="labels">
          {#each todo.labels as label}<span class="label">{label}</span>{/each}
        </span>
      {/if}
      {#if todo.dueDate}
        {@const overdue = !todo.completed && todo.dueDate < todayISO()}
        <span class="badge due {overdue ? 'overdue' : ''}"><Icon name="calendar" size={12} /> {todo.dueDate}</span>
      {/if}
      {#if todo.recurrence}
        <span class="badge recur" title={todo.recurrenceLabel}><Icon name="repeat" size={12} /> {todo.recurrence.fromCompletion ? 'every!' : 'every'} {todo.recurrenceLabel}</span>
      {/if}
      <span class="actions">
        <button class="ghost complete-btn" onclick={toggleCompleted} aria-label={todo.completed ? 'Mark incomplete' : 'Mark complete'}>
          <Icon name={todo.completed ? 'undo' : 'check'} size={16} />
        </button>
        <button class="ghost" onclick={startEdit} aria-label="Edit"><Icon name="edit" size={16} /></button>
        <button class="ghost" onclick={openLabelPicker} aria-label="Labels"><Icon name="tag" size={16} /></button>
        <button class="ghost" onclick={() => (addingChild = !addingChild)} aria-label="Add child"><Icon name="plus" size={16} /></button>
        <button class="ghost danger" onclick={onDelete} aria-label="Delete"><Icon name="trash" size={16} /></button>
      </span>
    </div>
    {#if expanded && todo.description}
      <div class="detail">{todo.description}</div>
    {/if}
  {/if}

  {#if labelPickerOpen}
    <div class="label-picker" role="dialog" aria-label="Labels">
      <div class="chips">
        {#each activeLabels as label (label)}
          <button type="button" class="chip removable" onclick={() => toggleExisting(label)}>
            {label}<span class="x">×</span>
          </button>
        {/each}
      </div>
      <form
        class="label-input"
        onsubmit={(e) => {
          e.preventDefault();
          commitLabelQuery();
        }}
      >
        <input
          type="text"
          bind:value={labelQuery}
          placeholder="Type a new label…"
          autocomplete="off"
          onkeydown={onLabelPickerKeydown}
          use:focus
        />
      </form>
      {#if labelSuggestions.length}
        <ul class="suggestions" role="listbox">
          {#each labelSuggestions as suggestion (suggestion)}
            <li>
              <button type="button" class="suggestion" onclick={() => toggleExisting(suggestion)}>
                + {suggestion}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="row">
        <button type="button" class="primary" onclick={saveLabels}>Save</button>
        <button type="button" onclick={() => (labelPickerOpen = false)}>Cancel</button>
        <button
          type="button"
          class="ghost"
          onclick={() => (managePredefinedOpen = !managePredefinedOpen)}
          aria-expanded={managePredefinedOpen}
        >
          {managePredefinedOpen ? '▾' : '▸'} Predefined
        </button>
      </div>
      {#if managePredefinedOpen}
        <div class="predefined-manager">
          <p class="hint">
            Predefined labels always appear in the suggestion list, even if no todo uses them.
          </p>
          <form
            class="label-input"
            onsubmit={(e) => {
              e.preventDefault();
              addPredefined();
            }}
          >
            <input
              type="text"
              bind:value={predefinedQuery}
              placeholder="New predefined label…"
              autocomplete="off"
            />
            <button type="submit" class="ghost">Add</button>
          </form>
          {#if store.labels.length}
            <ul class="predefined-list">
              {#each store.labels as label (label)}
                <li>
                  <span class="label">{label}</span>
                  <button
                    type="button"
                    class="ghost danger"
                    onclick={() => removePredefined(label)}
                    aria-label="Remove predefined label {label}"
                  >
                    ×
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}
    </div>
  {/if}

  {#if addingChild}
    <NewTodo placeholder="Add a subtask…" onAdd={addChild} onCancel={() => (addingChild = false)} />
  {/if}

  {#if children.length}
    <ul
      class="children"
      role="group"
      ondrop={onChildListDrop}
      ondragover={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {#each children as child (child.id)}
        <li><Self todo={child} /></li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .item {
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 8px 10px;
    margin: 4px 0;
  }
  .item.drop-before {
    border-top: 3px solid var(--accent);
  }
  .item.drop-after {
    border-bottom: 3px solid var(--accent);
  }
  .item.drop-into {
    background: var(--drop);
    border-color: var(--accent);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    cursor: pointer;
  }
  .title {
    font-weight: 500;
  }
  .done .title,
  .done .detail {
    text-decoration: line-through;
    color: var(--muted);
  }
  .detail {
    color: var(--muted);
    margin: 6px 0 2px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .disclosure {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 14px;
    color: var(--muted);
    transition: transform 0.15s ease;
  }
  .item.expanded > .head > .disclosure {
    transform: rotate(90deg);
  }
  .labels {
    display: inline-flex;
    gap: 4px;
  }
  .label {
    background: var(--drop);
    color: var(--accent-strong);
    border: 1px solid var(--accent-tint);
    border-radius: 999px;
    padding: 1px 8px;
    font-size: 12px;
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: var(--drop);
    color: var(--accent-strong);
    border: 1px solid var(--accent-tint);
    border-radius: 999px;
    padding: 1px 8px;
    font-size: 12px;
    white-space: nowrap;
  }
  .badge.overdue {
    background: var(--danger-tint);
    color: var(--danger);
    border-color: var(--danger);
  }
  .badge.recur {
    background: var(--recur-tint);
    color: var(--recur);
    border-color: var(--recur-line);
  }
  .actions {
    margin-left: auto;
    display: inline-flex;
    gap: 2px;
  }
  .actions button {
    padding: 2px 6px;
  }
  .danger:hover {
    color: var(--danger);
    border-color: var(--danger);
  }
  .done .complete-btn {
    color: var(--accent-strong);
    border-color: var(--accent);
  }
  .edit {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .edit .row {
    display: flex;
    gap: 6px;
  }
  .schedule {
    font-size: 12px;
  }
  .rc-feedback {
    font-size: 12px;
    margin: -2px 0;
  }
  .rc-feedback.ok {
    color: var(--muted);
  }
  .rc-feedback.err {
    color: var(--danger);
  }
  .children {
    margin-left: 18px;
    margin-top: 6px;
    padding-left: 12px;
    border-left: 2px dashed var(--line);
  }
  .label-picker {
    margin-top: 8px;
    border: 1px solid var(--accent);
    background: var(--raised);
    border-radius: 8px;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    box-shadow: 0 4px 14px var(--shadow);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    min-height: 4px;
  }
  .chip {
    background: var(--drop);
    color: var(--accent-strong);
    border: 1px solid var(--accent-tint);
    border-radius: 999px;
    padding: 2px 8px;
    font-size: 12px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .chip .x {
    font-weight: 700;
    opacity: 0.7;
  }
  .chip.removable:hover {
    background: var(--danger-tint);
    color: var(--danger);
    border-color: var(--danger);
  }
  .label-input {
    display: flex;
  }
  .suggestions {
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    max-height: 140px;
    overflow: auto;
  }
  .suggestion {
    width: 100%;
    text-align: left;
    border: none;
    background: transparent;
    border-radius: 0;
    padding: 4px 8px;
  }
  .suggestion:hover {
    background: var(--drop);
    color: var(--accent-strong);
  }
  .label-picker .row {
    display: flex;
    gap: 6px;
  }
  .predefined-manager {
    border-top: 1px solid var(--line);
    margin-top: 6px;
    padding-top: 6px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .predefined-manager .hint {
    margin: 0;
    font-size: 12px;
    opacity: 0.7;
  }
  .predefined-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 140px;
    overflow: auto;
    border: 1px solid var(--line);
    border-radius: 6px;
  }
  .predefined-list li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 2px 6px;
    gap: 6px;
  }
  .predefined-list li + li {
    border-top: 1px solid var(--line);
  }
</style>
