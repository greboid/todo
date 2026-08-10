<script>
  import { store } from './store.svelte.js';
  import Icon from './Icon.svelte';

  let helpOpen = $state(false);
</script>

<div class="filter">
  <input
    type="text"
    class="search"
    class:invalid={store.filterError}
    placeholder="Filter: !has:complete label:work priority:high date:week …  (or just type to search)"
    value={store.filterText}
    oninput={(e) => store.setFilterText(e.target.value)}
  />
  {#if store.filterActive}
    <button class="ghost clear" title="Clear filter" onclick={() => store.clearFilter()}>
      <Icon name="close" size={14} />
    </button>
  {/if}
  <button
    class="ghost help"
    class:open={helpOpen}
    title="Filter syntax help"
    aria-expanded={helpOpen}
    onclick={() => (helpOpen = !helpOpen)}
  >
    <Icon name="chevron" size={14} />
  </button>
  {#if store.filterError}
    <span class="filter-error" role="alert">{store.filterError}</span>
  {/if}
  {#if helpOpen}
    <!-- Transparent full-screen layer captures outside clicks to dismiss. -->
    <button class="backdrop" aria-label="Close filter help" onclick={() => (helpOpen = false)}></button>
    <div class="help-popover" role="dialog" aria-label="Filter syntax">
      <dl>
        <dt><code>label:<em>name</em></code></dt>
        <dd>has a label (OR when repeated)</dd>
        <dt><code>priority:<em>name</em></code></dt>
        <dd>has a priority (OR when repeated)</dd>
        <dt><code>date:<em>v</em></code></dt>
        <dd>
          due date: <code>week</code>, <code>overdue</code>, <code>none</code>, <code>today</code>,
          <code>tomorrow</code>, <code>YYYY-MM-DD</code>, or a <code>YYYY-MM-DD..YYYY-MM-DD</code> range
        </dd>
        <dt><code>has:<em>x</em></code></dt>
        <dd>existence: <code>complete</code>, <code>label</code>, <code>priority</code>, <code>recur</code>, <code>date</code></dd>
        <dt><code>!<em>key</em>:<em>v</em></code></dt>
        <dd>negate <code>label</code>, <code>priority</code>, <code>date</code>, or <code>has</code></dd>
        <dt><em>text</em></dt>
        <dd>search title + description</dd>
      </dl>
      <p class="hint">Default <code>!has:complete</code> hides completed. Invalid tokens show an error.</p>
    </div>
  {/if}
</div>

<style>
  .filter {
    display: flex;
    align-items: center;
    gap: 6px;
    position: relative;
  }
  .search {
    flex: 1;
    min-width: 0;
  }
  .search.invalid {
    outline: 2px solid var(--danger);
    outline-offset: -1px;
    border-color: var(--danger);
  }
  .clear,
  .help {
    flex-shrink: 0;
  }
  .help :global(.icon) {
    transition: transform 0.12s ease;
  }
  .help.open :global(.icon) {
    transform: rotate(90deg);
  }
  .filter-error {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    margin-top: 4px;
    color: var(--danger);
    background: var(--danger-tint);
    border: 1px solid var(--danger);
    border-radius: 6px;
    padding: 4px 8px;
    font-size: 12px;
    line-height: 1.4;
    z-index: 20;
  }
  .backdrop {
    position: fixed;
    inset: 0;
    background: transparent;
    border: none;
    padding: 0;
    z-index: 15;
  }
  .help-popover {
    position: absolute;
    top: 100%;
    right: 0;
    margin-top: 4px;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    padding: 10px 12px;
    z-index: 20;
    width: 320px;
    max-width: calc(100vw - 40px);
  }
  .help-popover dl {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .help-popover dt {
    color: var(--text);
    font-size: 12px;
  }
  .help-popover dd {
    margin: 0 0 0 12px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.4;
  }
  .help-popover code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 11px;
  }
  .hint {
    margin: 8px 0 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
  }
  .hint code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 10px;
  }
</style>
