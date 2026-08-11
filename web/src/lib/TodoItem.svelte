<script>
  import { store, labelColor } from './store.svelte.js';
  import { marked } from 'marked';
  import DOMPurify from 'dompurify';
  import NewTodo from './NewTodo.svelte';
  import Icon from './Icon.svelte';
  import Self from './TodoItem.svelte';
  import { focus } from './actions.js';
  import { api } from './api.js';

  marked.use({
    gfm: true,
    breaks: true,
    renderer: {
      link({ href, tokens }) {
        const text = this.parser.parseInline(tokens);
        return `<a href="${href}" target="_blank" rel="noopener noreferrer">${text}</a>`;
      },
    },
  });
  function renderDescription(md) {
    const raw = marked.parse(md ?? '', { async: false });
    return DOMPurify.sanitize(raw, {
      ALLOWED_TAGS: ['p', 'br', 'strong', 'em', 'del', 'code', 'pre', 'ul', 'ol', 'li', 'blockquote', 'a', 'hr'],
      ALLOWED_ATTR: ['href', 'target', 'rel'],
      ALLOW_DATA_ATTR: false,
    });
  }

  let { todo } = $props();
  // editing is driven by the global single-edit slot in the store.
  let editing = $derived(store.editingId === todo.id);
  let draftTitle = $state('');
  let draftDesc = $state('');
  let draftLabels = $state([]);
  let draftPriority = $state('');
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

  // Inline priority picker state.
  let priorityPickerOpen = $state(false);
  let activePriority = $state('');

  // Drop target state for this row. Dragging is initiated from the drag
  // handle (which is its own draggable element); the row itself is never
  // draggable so clicks, text selection, and buttons always work. The row is
  // only a drop target.
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
      .filter((l) => !activeLabels.includes(l.name) && l.name.toLowerCase().includes(labelQuery.toLowerCase()))
      .slice(0, 8),
  );

  function onHeadClick(e) {
    // Nothing to reveal when there's no description; double-click still edits.
    if (!todo.description) return;
    // Ignore clicks on the checkbox, buttons, the action cluster, or the drag
    // handle (those are interactive controls, not expand toggles).
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'BUTTON' || tag === 'TEXTAREA' || e.target.closest('.actions')) return;
    if (e.target.closest('.drag-handle')) return;
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
    draftLabels = [...(todo.labels || [])];
    draftPriority = todo.priority || '';
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
    const patch = {
      title: draftTitle.trim() || todo.title,
      description: draftDesc,
      labels: [...draftLabels],
      priority: draftPriority,
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

  async function addChild(payload) {
    await store.create({ ...payload, parentId: todo.id });
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

  function storedColor(name) {
    const found = store.labels.find((l) => l.name === name);
    return found ? found.color : '';
  }

  // --- Edit-form label chips ---
  let editLabelQuery = $state('');
  const editLabelSuggestions = $derived(
    store.labels
      .filter((l) => !draftLabels.includes(l.name) && l.name.toLowerCase().includes(editLabelQuery.toLowerCase()))
      .slice(0, 6),
  );

  function addEditLabel(name) {
    name = name.trim();
    if (!name) return;
    if (!draftLabels.some((l) => l.toLowerCase() === name.toLowerCase())) {
      draftLabels = [...draftLabels, name];
    }
    editLabelQuery = '';
  }

  function removeEditLabel(name) {
    draftLabels = draftLabels.filter((l) => l !== name);
  }

  function onEditLabelKeydown(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (editLabelSuggestions.length === 1) {
        addEditLabel(editLabelSuggestions[0].name);
      } else {
        addEditLabel(editLabelQuery);
      }
    } else if (e.key === 'Backspace' && editLabelQuery === '' && draftLabels.length) {
      draftLabels = draftLabels.slice(0, -1);
    }
  }

  function openPriorityPicker() {
    activePriority = todo.priority || '';
    priorityPickerOpen = true;
  }

  function selectPriority(name) {
    activePriority = activePriority === name ? '' : name;
  }

  async function savePriority() {
    await store.update(todo.id, { priority: activePriority });
    priorityPickerOpen = false;
  }

  function storedPriorityColor(name) {
    const found = store.priorities.find((p) => p.name === name);
    return found ? found.color : '';
  }

  // --- Edit-form priority selection ---
  function toggleDraftPriority(name) {
    draftPriority = draftPriority === name ? '' : name;
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
      // Nest as a child. No position => the server appends at the end of all
      // children (hidden ones included), which is visually last.
      store.move(id, { parentId: todo.id });
    } else {
      // Drop before/after this todo. Use its stored (absolute) position so the
      // target stays correct even when the filter hides siblings. Root todos
      // serialize with omitempty parentId, so coerce undefined -> null for an
      // explicit move-to-root.
      store.move(id, {
        parentId: todo.parentId ?? null,
        position: store.dropPosition(id, todo, zone),
      });
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
    store.move(id, { parentId: todo.id });
  }

  let classes = $derived(
    `item ${todo.completed ? 'done' : ''} ${dropZone ? `drop drop-${dropZone}` : ''} ${expanded ? 'expanded' : ''}`,
  );
</script>

<div
  bind:this={rootEl}
  class={classes}
  role="treeitem"
  tabindex="0"
  aria-selected={todo.completed}
  aria-expanded={children.length > 0}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={resolveDrop}
>
  {#if editing}
    <form class="edit" onsubmit={(e) => { e.preventDefault(); saveEdit(); }}>
      <input type="text" bind:value={draftTitle} oninput={store.markEditDirty} onkeydown={onEditKeydown} use:focus />
      <textarea bind:value={draftDesc} rows="2" placeholder="Description" oninput={store.markEditDirty} onkeydown={onEditKeydown}></textarea>
      <div class="chip-field">
        <div class="chips">
          {#each draftLabels as label (label)}
            <button
              type="button"
              class="chip removable"
              style:background={labelColor(label, storedColor(label)) + '22'}
              style:border-color={labelColor(label, storedColor(label))}
              style:color={labelColor(label, storedColor(label))}
              onclick={() => removeEditLabel(label)}
            >
              {label}<span class="x">×</span>
            </button>
          {/each}
        </div>
        <input
          type="text"
          bind:value={editLabelQuery}
          placeholder={draftLabels.length ? '' : 'Labels…'}
          oninput={store.markEditDirty}
          onkeydown={onEditLabelKeydown}
        />
        {#if editLabelSuggestions.length && editLabelQuery}
          <ul class="inline-suggestions">
            {#each editLabelSuggestions as s (s.name)}
              <li>
                <button type="button" class="inline-suggestion" onclick={() => addEditLabel(s.name)}>
                  <span class="suggestion-dot" style:background={labelColor(s.name, s.color)}></span>
                  {s.name}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      {#if store.priorities.length}
        <div class="priority-chips">
          <span class="field-label"><Icon name="flag" size={12} /></span>
          {#each store.priorities as p (p.name)}
            <button
              type="button"
              class="chip {draftPriority === p.name ? 'selected' : ''}"
              style:background={draftPriority === p.name ? labelColor(p.name, p.color) + '22' : 'transparent'}
              style:border-color={draftPriority === p.name ? labelColor(p.name, p.color) : 'var(--line)'}
              style:color={draftPriority === p.name ? labelColor(p.name, p.color) : 'var(--text)'}
              onclick={() => toggleDraftPriority(p.name)}
            >
              {p.name}
            </button>
          {/each}
        </div>
      {/if}
      <input
        type="text"
        class="schedule"
        bind:value={draftSchedule}
        placeholder="Due date or repeat, e.g. aug 15, every 2 weeks on mon, wed, every month on the 15th starting sep 1"
        oninput={store.markEditDirty}
        onkeydown={onEditKeydown}
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
      <span
        class="drag-handle"
        role="button"
        tabindex="-1"
        aria-label="Drag to reorder"
        title="Drag to reorder"
        draggable={!editing}
        ondragstart={onDragStart}
        ondragend={onDragEnd}
      >☰
      </span>
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
          {#each todo.labels as label (label)}
            <span
              class="label"
              style:background={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '') + '22'}
              style:border-color={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '')}
              style:color={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '')}
            >{label}</span>
          {/each}
        </span>
      {/if}
      {#if todo.priority}
        <span
          class="badge priority"
          style:background={labelColor(todo.priority, todo.priorityColor || '') + '22'}
          style:border-color={labelColor(todo.priority, todo.priorityColor || '')}
          style:color={labelColor(todo.priority, todo.priorityColor || '')}
        ><Icon name="flag" size={12} /> {todo.priority}</span>
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
        <button class="ghost" onclick={openPriorityPicker} aria-label="Priority"><Icon name="flag" size={16} /></button>
        <button class="ghost" onclick={() => (addingChild = !addingChild)} aria-label="Add child"><Icon name="plus" size={16} /></button>
        <button class="ghost danger" onclick={onDelete} aria-label="Delete"><Icon name="trash" size={16} /></button>
      </span>
    </div>
    {#if expanded && todo.description}
      <div class="detail">{@html renderDescription(todo.description)}</div>
    {/if}
  {/if}

  {#if labelPickerOpen}
    <div class="label-picker" role="dialog" aria-label="Labels">
      <div class="chips">
        {#each activeLabels as label (label)}
          <button
            type="button"
            class="chip removable"
            style:background={labelColor(label, storedColor(label)) + '22'}
            style:border-color={labelColor(label, storedColor(label))}
            style:color={labelColor(label, storedColor(label))}
            onclick={() => toggleExisting(label)}
          >
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
          {#each labelSuggestions as suggestion (suggestion.name)}
            <li>
              <button
                type="button"
                class="suggestion"
                onclick={() => toggleExisting(suggestion.name)}
              >
                <span class="suggestion-dot" style:background={labelColor(suggestion.name, suggestion.color)}></span>
                {suggestion.name}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="row">
        <button type="button" class="primary" onclick={saveLabels}>Save</button>
        <button type="button" onclick={() => (labelPickerOpen = false)}>Cancel</button>
      </div>
    </div>
  {/if}

  {#if priorityPickerOpen}
    <div class="label-picker" role="dialog" aria-label="Priority">
      {#if activePriority}
        <div class="chips">
          <button
            type="button"
            class="chip removable"
            style:background={labelColor(activePriority, storedPriorityColor(activePriority)) + '22'}
            style:border-color={labelColor(activePriority, storedPriorityColor(activePriority))}
            style:color={labelColor(activePriority, storedPriorityColor(activePriority))}
            onclick={() => (activePriority = '')}
          >
            {activePriority}<span class="x">×</span>
          </button>
        </div>
      {/if}
      {#if store.priorities.length}
        <ul class="suggestions" role="listbox">
          {#each store.priorities as p (p.name)}
            <li>
              <button
                type="button"
                class="suggestion"
                class:selected={activePriority === p.name}
                onclick={() => selectPriority(p.name)}
              >
                <span class="suggestion-dot" style:background={labelColor(p.name, p.color)}></span>
                {p.name}
                {#if activePriority === p.name}<span class="check">✓</span>{/if}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="row">
        <button type="button" class="primary" onclick={savePriority}>Save</button>
        <button type="button" onclick={() => (priorityPickerOpen = false)}>Cancel</button>
      </div>
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
    word-break: break-word;
  }
  .detail :global(a) {
    color: inherit;
    text-decoration: underline;
  }
  .detail :global(code) {
    font-family: ui-monospace, monospace;
    background: var(--bg-elevated, rgba(0, 0, 0, 0.05));
    border-radius: 3px;
    padding: 0 4px;
  }
  .detail :global(pre) {
    margin: 6px 0;
    padding: 8px;
    overflow-x: auto;
  }
  .detail :global(pre code) {
    background: none;
    padding: 0;
  }
  .detail :global(ul),
  .detail :global(ol) {
    margin: 6px 0;
    padding-left: 1.4em;
  }
  .detail :global(blockquote) {
    margin: 6px 0;
    padding-left: 0.8em;
    border-left: 3px solid var(--border, rgba(0, 0, 0, 0.1));
  }
  .detail :global(p) {
    margin: 4px 0;
  }
  .detail > :global(*:first-child) {
    margin-top: 0;
  }
  .detail > :global(*:last-child) {
    margin-bottom: 0;
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
  .drag-handle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 16px;
    cursor: grab;
    color: var(--muted);
    font-size: 16px;
    line-height: 1;
    opacity: 0.5;
    user-select: none;
    -webkit-user-select: none;
  }
  .head:hover .drag-handle {
    opacity: 1;
  }
  .drag-handle:active {
    cursor: grabbing;
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
  .suggestion .check {
    margin-left: auto;
    font-weight: 700;
  }
  .suggestion {
    display: flex;
    align-items: center;
    gap: 6px;
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
  .chip-field {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    align-items: center;
    border: 1px solid var(--line);
    border-radius: 6px;
    padding: 4px;
    background: var(--panel);
  }
  .chip-field .chips {
    gap: 4px;
    flex-wrap: wrap;
    align-items: center;
  }
  .chip-field input {
    flex: 1;
    min-width: 80px;
    border: none;
    background: transparent;
    padding: 2px 4px;
    outline: none;
  }
  .chip-field input:focus {
    outline: none;
  }
  .inline-suggestions {
    position: absolute;
    margin-top: 2px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    z-index: 10;
    max-height: 140px;
    overflow: auto;
    list-style: none;
    padding: 2px;
  }
  .inline-suggestion {
    width: 100%;
    text-align: left;
    border: none;
    background: transparent;
    border-radius: 4px;
    padding: 4px 8px;
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
  }
  .inline-suggestion:hover {
    background: var(--drop);
  }
  .priority-chips {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
  }
  .priority-chips .field-label {
    display: inline-flex;
    align-items: center;
    color: var(--muted);
  }
  .priority-chips .chip {
    cursor: pointer;
  }
  .priority-chips .chip.selected {
    font-weight: 600;
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
  .suggestion-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    margin-right: 4px;
    vertical-align: middle;
  }
  .suggestion {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }
</style>
