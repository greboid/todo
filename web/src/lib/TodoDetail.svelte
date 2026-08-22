<script>
  import { store, labelColor } from './store.svelte.js';
  import { renderDescription } from './markdown.js';
  import Icon from './Icon.svelte';

  // The projected list is live (offline changes and syncs land in it as they
  // happen), so prefer it whenever the todo is visible there; otherwise the
  // snapshot fetched for this page is the best view available.
  const todo = $derived(store.byId(store.detailId) ?? store.detailTodo);
  const board = $derived(store.boardById(todo?.boardId));
  const parent = $derived(store.detailParents[store.detailParents.length - 1] ?? null);
  const overdue = $derived(todo && !todo.completed && !!todo.dueDate && todo.dueDate < todayISO());

  function todayISO() {
    const d = new Date();
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }

  // RFC3339 timestamps (createdAt/completedAt) shown as a local date+time.
  function formatStamp(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  // Due dates are plain days: parse with a time component so they read in
  // local time, not UTC.
  function formatDue(day) {
    const d = new Date(`${day}T00:00:00`);
    if (Number.isNaN(d.getTime())) return day;
    return d.toLocaleDateString(undefined, { dateStyle: 'medium' });
  }

  // Close the page and, when the todo lives on another board, switch to it —
  // the natural "take me there" affordance for cross-board todos.
  function goToBoard() {
    const id = todo?.boardId;
    store.closeDetail();
    if (id != null && id !== store.activeBoardId) store.selectBoard(id);
  }
</script>

<div class="detail-page" role="article">
  {#if store.detailLoading && !todo}
    <p class="muted">Loading…</p>
  {:else if store.detailError && !todo}
    <div class="fallback">
      <p class="muted">{store.detailError}</p>
      <button class="ghost" onclick={() => store.closeDetail()}>
        <Icon name="chevron" size={14} /> Back to list
      </button>
    </div>
  {:else if todo}
    <div class="detail-top">
      <button class="ghost back" onclick={() => store.closeDetail()} title="Back to list">
        <Icon name="chevron" size={16} /> Back
      </button>
      {#if todo.pending}
        <span class="pending-note" title="Queued while offline; syncs when the connection returns.">Pending sync</span>
      {/if}
    </div>

    <div class="card" class:done={todo.completed}>
      <div class="head">
        <input
          type="checkbox"
          checked={todo.completed}
          aria-label={todo.completed ? 'Mark as not done' : 'Mark as done'}
          onchange={(e) => store.setDetailCompleted(e.currentTarget.checked)}
        />
        <h2 class="title">{todo.title}</h2>
      </div>

      {#if store.detailError}
        <p class="error">{store.detailError}</p>
      {/if}

      {#if store.detailParents.length}
        <nav class="breadcrumb" aria-label="Parent todos">
          <span class="muted">In:</span>
          {#each store.detailParents as p, i (p.id)}
            {#if i > 0}<span class="sep" aria-hidden="true">›</span>{/if}
            <button class="ghost crumb" onclick={() => store.openDetail(p.id)}>{p.title}</button>
          {/each}
        </nav>
      {/if}

      <dl class="facts">
        <div class="fact">
          <dt>Board</dt>
          <dd>
            <button class="ghost fact-value board-link" onclick={goToBoard}>
              <Icon name="board" size={12} /> {board?.name ?? `Board ${todo.boardId}`}
            </button>
          </dd>
        </div>
        {#if todo.priority}
          <div class="fact">
            <dt>Priority</dt>
            <dd>
              <span
                class="badge priority"
                style:background={labelColor(todo.priority, todo.priorityColor || '') + '22'}
                style:border-color={labelColor(todo.priority, todo.priorityColor || '')}
                style:color={labelColor(todo.priority, todo.priorityColor || '')}
              ><Icon name="flag" size={12} /> {todo.priority}</span>
            </dd>
          </div>
        {/if}
        {#if todo.labels?.length}
          <div class="fact">
            <dt>Labels</dt>
            <dd class="labels">
              {#each todo.labels as label (label)}
                <span
                  class="badge"
                  style:background={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '') + '22'}
                  style:border-color={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '')}
                  style:color={labelColor(label, todo.labelColors?.find((lc) => lc.name === label)?.color || '')}
                >{label}</span>
              {/each}
            </dd>
          </div>
        {/if}
        {#if todo.dueDate}
          <div class="fact">
            <dt>Due</dt>
            <dd>
              <span class="badge due {overdue ? 'overdue' : ''}">
                <Icon name="calendar" size={12} /> {formatDue(todo.dueDate)}
              </span>
            </dd>
          </div>
        {/if}
        {#if todo.recurrence}
          <div class="fact">
            <dt>Repeats</dt>
            <dd>
              <span class="badge recur" title={todo.recurrenceLabel}>
                <Icon name="repeat" size={12} />
                {todo.recurrence.fromCompletion ? 'every! ' : 'every '}{todo.recurrenceLabel}
              </span>
            </dd>
          </div>
        {/if}
        {#if todo.createdAt}
          <div class="fact">
            <dt>Created</dt>
            <dd class="fact-value">{formatStamp(todo.createdAt)}</dd>
          </div>
        {/if}
        {#if todo.completedAt}
          <div class="fact">
            <dt>Completed</dt>
            <dd class="fact-value">{formatStamp(todo.completedAt)}</dd>
          </div>
        {/if}
      </dl>

      {#if todo.description}
        <div class="description">{@html renderDescription(todo.description)}</div>
      {/if}
    </div>

    {#if store.detailChildren.length}
      <section class="subtasks">
        <h3>Subtasks</h3>
        <ul role="list">
          {#each store.detailChildren as child (child.id)}
            <li>
              <button class="subtask" onclick={() => store.openDetail(child.id)} title="View details">
                <span class="state" class:is-done={child.completed} aria-hidden="true">
                  {#if child.completed}<Icon name="check" size={11} />{/if}
                </span>
                <span class="subtask-title" class:is-done={child.completed}>{child.title}</span>
              </button>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
</div>

<style>
  .detail-page {
    max-width: 720px;
    margin: 0 auto;
    padding: 12px 20px 48px;
  }
  .muted {
    color: var(--muted);
  }
  .error {
    color: var(--danger);
    margin: 0;
  }
  .detail-top {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }
  .back :global(.icon),
  .fallback :global(.icon) {
    transform: rotate(180deg);
  }
  /* Queued offline change: the server hasn't confirmed it yet (same signal
     as the list's dashed-border rows). */
  .pending-note {
    font-size: 12px;
    color: var(--muted);
    background: var(--raised);
    border: 1px dashed var(--muted);
    border-radius: 999px;
    padding: 2px 8px;
  }
  .fallback {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
    padding: 24px 0;
  }
  .card {
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .head {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }
  .head input {
    align-self: center;
    flex-shrink: 0;
  }
  .title {
    margin: 0;
    font-size: 18px;
    word-break: break-word;
  }
  .done .title,
  .done .description {
    text-decoration: line-through;
    color: var(--muted);
  }
  .breadcrumb {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    font-size: 13px;
  }
  .crumb {
    padding: 2px 4px;
    font-size: 13px;
  }
  .sep {
    color: var(--muted);
  }
  .facts {
    margin: 0;
    display: grid;
    grid-template-columns: minmax(72px, auto) 1fr;
    gap: 6px 12px;
    font-size: 13px;
  }
  .fact {
    display: contents;
  }
  .fact dt {
    color: var(--muted);
    white-space: nowrap;
  }
  .fact dd {
    margin: 0;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .fact-value {
    word-break: break-word;
  }
  .board-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 1px 4px;
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
  .description {
    color: var(--text);
    word-break: break-word;
    border-top: 1px solid var(--line);
    padding-top: 12px;
  }
  .description :global(a) {
    color: inherit;
    text-decoration: underline;
  }
  .description :global(code) {
    font-family: ui-monospace, monospace;
    background: rgba(0, 0, 0, 0.05);
    border-radius: 3px;
    padding: 0 4px;
  }
  .description :global(pre) {
    margin: 6px 0;
    padding: 8px;
    overflow-x: auto;
  }
  .description :global(pre code) {
    background: none;
    padding: 0;
  }
  .description :global(ul),
  .description :global(ol) {
    margin: 6px 0;
    padding-left: 1.4em;
  }
  .description :global(blockquote) {
    margin: 6px 0;
    padding-left: 0.8em;
    border-left: 3px solid var(--line);
  }
  .description :global(p) {
    margin: 4px 0;
  }
  .description > :global(*:first-child) {
    margin-top: 0;
  }
  .description > :global(*:last-child) {
    margin-bottom: 0;
  }
  .subtasks {
    margin-top: 16px;
  }
  .subtasks h3 {
    margin: 0 0 8px;
    font-size: 13px;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .subtask {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 6px 10px;
    text-align: left;
    background: var(--panel);
  }
  .subtasks li + li {
    margin-top: 4px;
  }
  .state {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 14px;
    height: 14px;
    border: 1px solid var(--line);
    border-radius: 4px;
    color: var(--accent-strong);
  }
  .state.is-done {
    background: var(--accent-tint);
    border-color: var(--accent);
  }
  .subtask-title {
    word-break: break-word;
  }
  .subtask-title.is-done {
    color: var(--muted);
    text-decoration: line-through;
  }

  @media (max-width: 720px) {
    .detail-page {
      padding: 12px 12px 48px;
    }
    .card {
      padding: 12px;
    }
  }
</style>
