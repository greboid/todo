<script>
  import { focus } from './actions.js';
  import { api } from './api.js';

  let { placeholder = 'Add a todo…', onAdd, onCancel } = $props();

  let text = $state('');

  // The whole quick-add grammar (labels via #tag, trailing due/recurrence)
  // lives server-side behind POST /api/schedule/extract. The UI is a thin
  // caller: it shows a debounced preview of what will be created and, on
  // submit, passes the endpoint's result straight through to onAdd.
  let preview = $state(null);
  let previewSeq = 0;
  $effect(() => {
    const v = text.trim();
    if (!v) {
      preview = null;
      return;
    }
    const seq = ++previewSeq;
    const handle = setTimeout(async () => {
      const res = await api.extractSchedule(v).catch(() => null);
      if (seq !== previewSeq) return;
      preview = res && res.ok ? { ...res, src: v } : null;
    }, 250);
    return () => clearTimeout(handle);
  });

  // Feedback line: the parsed labels (#chips) and schedule arrow, shown only
  // while it matches the current text (null while a parse is pending).
  let feedback = $derived.by(() => {
    const v = text.trim();
    if (!v || !preview || preview.src !== v) return null;
    const parts = [];
    if (preview.labels?.length) parts.push(preview.labels.map((l) => `#${l}`).join(' '));
    if (preview.scheduleText) parts.push(`→ ${preview.scheduleText}`);
    return parts.length ? parts.join('   ') : null;
  });
  let canSubmit = $derived(text.trim().length > 0);

  async function submit(e) {
    e.preventDefault();
    const v = text.trim();
    if (!v) return;
    try {
      const res = await api.extractSchedule(v);
      if (!res || !res.ok || !res.title) return; // no usable title: keep the text
      const payload = { title: res.title };
      if (res.labels?.length) payload.labels = res.labels;
      if (res.dueDate) payload.dueDate = res.dueDate;
      if (res.recurrence) payload.recurrence = res.recurrence;
      await onAdd?.(payload);
      text = '';
      preview = null;
    } catch {
      /* leave the text so the user can retry */
    }
  }

  function cancel(e) {
    if (e.key === 'Escape') {
      text = '';
      onCancel ? onCancel() : e.target.blur();
    }
  }
</script>

<form class="row" onsubmit={submit}>
  <input type="text" bind:value={text} {placeholder} onkeydown={cancel} use:focus />
  <button type="submit" class="primary" disabled={!canSubmit}>Add</button>
  {#if feedback}
    <span class="preview">{feedback}</span>
  {/if}
</form>

<style>
  .row {
    display: flex;
    gap: 8px;
    padding: 12px 20px;
    align-items: center;
  }
  .preview {
    font-size: 12px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
