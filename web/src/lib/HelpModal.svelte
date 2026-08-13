<script>
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  let { onClose } = $props();

  function onKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose?.();
    }
  }
</script>

<button class="backdrop" aria-label="Close" onclick={() => onClose?.()}></button>

<div class="modal" role="dialog" aria-label="Filter syntax help" tabindex="-1" use:focus onkeydown={onKeydown}>
  <div class="modal-header">
    <h2><Icon name="help" size={18} /> Filter syntax</h2>
    <button class="ghost close-btn" onclick={() => onClose?.()} aria-label="Close">
      <Icon name="close" size={18} />
    </button>
  </div>

  <dl>
    <dt><code>label:<em>name</em></code></dt>
    <dd>has a label (OR when repeated)</dd>
    <dt><code>priority:<em>name</em></code></dt>
    <dd>has a priority (OR when repeated)</dd>
    <dt><code>date:<em>v</em></code></dt>
    <dd>
      due date: <code>week</code>, <code>overdue</code>, <code>none</code>, <code>today</code>,
      <code>tomorrow</code>, <code>YYYY-MM-DD</code>, or a <code>YYYY-MM-DD..YYYY-MM-DD</code> range.
      Combine presets with <code>or</code>/<code>and</code> in one value, e.g.
      <code>date:"overdue or today"</code>. Only one <code>date:</code> per query.
    </dd>
    <dt><code>has:<em>x</em></code></dt>
    <dd>existence: <code>complete</code>, <code>label</code>, <code>priority</code>, <code>recur</code>, <code>date</code></dd>
    <dt><code>sort:<em>field</em></code></dt>
    <dd>
      order siblings by <code>priority</code>, <code>label</code>, or <code>date</code>; repeat
      for tiebreakers (first wins). Prefix <code>!</code> to reverse, e.g. <code>sort:!priority</code>
    </dd>
    <dt><code>!<em>key</em>:<em>v</em></code></dt>
    <dd>negate <code>label</code>, <code>priority</code>, <code>date</code>, or <code>has</code></dd>
    <dt><em>text</em></dt>
    <dd>search title + description</dd>
    <dt><code>#<em>label</em></code> <code>@<em>priority</em></code></dt>
    <dd>quick-add tags in the new-todo field (e.g. <code>Buy milk #errands @high tomorrow</code>)</dd>
  </dl>
  <p class="hint">Default <code>!has:complete</code> hides completed. Invalid tokens show an error.</p>
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
  dl {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  dt {
    color: var(--text);
    font-size: 13px;
  }
  dd {
    margin: 0 0 0 12px;
    color: var(--muted);
    font-size: 13px;
    line-height: 1.5;
  }
  code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 12px;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.4;
  }
  .hint code {
    background: var(--raised);
    border-radius: 3px;
    padding: 0 3px;
    font-size: 11px;
  }
</style>
