<script>
  import { focus } from './actions.js';

  let { placeholder = 'Add a todo…', onAdd, onCancel } = $props();

  let title = $state('');

  function submit(e) {
    e.preventDefault();
    const v = title.trim();
    if (!v) return;
    onAdd?.(v);
    title = '';
  }

  function cancel(e) {
    if (e.key === 'Escape') {
      title = '';
      onCancel ? onCancel() : e.target.blur();
    }
  }
</script>

<form class="row" onsubmit={submit}>
  <input type="text" bind:value={title} {placeholder} onkeydown={cancel} use:focus />
  <button type="submit" class="primary" disabled={!title.trim()}>Add</button>
</form>

<style>
  .row {
    display: flex;
    gap: 8px;
    padding: 12px 20px;
  }
</style>
