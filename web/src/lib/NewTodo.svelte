<script>
  import { focus } from './actions.js';
  import { api } from './api.js';
  import { store } from './store.svelte.js';

  let { placeholder = 'Add a todo…', onAdd, onCancel } = $props();

  let text = $state('');
  let inputEl = $state(null);

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

  // Feedback line: the parsed labels (#chips), priority (!flag), and schedule
  // arrow, shown only while it matches the current text (null while a parse
  // is pending).
  let feedback = $derived.by(() => {
    const v = text.trim();
    if (!v || !preview || preview.src !== v) return null;
    const parts = [];
    if (preview.labels?.length) parts.push(preview.labels.map((l) => `#${l}`).join(' '));
    if (preview.priority) parts.push(`@${preview.priority}`);
    if (preview.scheduleText) parts.push(`→ ${preview.scheduleText}`);
    return parts.length ? parts.join('   ') : null;
  });
  let canSubmit = $derived(text.trim().length > 0);

  // Debounced blanking: feedback drops to null on every keystroke (the
  // preview is stale) and comes back once the debounced server parse lands,
  // which makes the line flicker. Keep showing the last value for a grace
  // period instead; clear immediately when the text is emptied (submit,
  // Escape) so the reset still feels instant.
  const FEEDBACK_HOLD_MS = 2000;
  let shownFeedback = $state(null);
  $effect(() => {
    if (feedback !== null) {
      shownFeedback = feedback;
      return;
    }
    if (!text.trim()) {
      shownFeedback = null;
      return;
    }
    const handle = setTimeout(() => (shownFeedback = null), FEEDBACK_HOLD_MS);
    return () => clearTimeout(handle);
  });

  // -- Label tab completion with popup --
  // After typing "#" and some characters, a dropdown lists matching labels.
  // Tab / ArrowDown cycles forward, Shift+Tab / ArrowUp cycles back; Enter or
  // click commits the highlighted match; Escape dismisses the dropdown. The
  // completed token replaces the partial after the "#".
  let labelState = $state({ active: false, index: 0 });

  function labelMatches(query) {
    const q = query.toLowerCase();
    const names = (store.labels ?? []).map((l) => l.name);
    const seen = new Set();
    const out = [];
    for (const n of names) {
      if (seen.has(n)) continue;
      seen.add(n);
      if (n.toLowerCase().startsWith(q)) out.push(n);
    }
    out.sort((a, b) => a.localeCompare(b));
    return out;
  }

  function priorityMatches(query) {
    const q = query.toLowerCase();
    return (store.priorities ?? [])
      .map((p) => p.name)
      .filter((n) => n.toLowerCase().startsWith(q))
      .sort((a, b) => a.localeCompare(b));
  }

  // Inspect the current caret position for a "#partial" or "!partial" token
  // being typed. Returns { start, end, partial, prefix } where [start,end)
  // covers the word chars after the prefix marker, or null when not inside a
  // token.
  function currentToken(el) {
    if (!el) return null;
    const pos = el.selectionStart;
    if (pos !== el.selectionEnd) return null;
    const before = text.slice(0, pos);
    const m = /(?:^|\s)([#@])([\w-]*)$/.exec(before);
    if (!m) return null;
    const prefix = m[1];
    const partial = m[2];
    const start = m.index + (m[0].length - partial.length);
    return { start, end: pos, partial, prefix };
  }

  let suggestions = $derived.by(() => {
    const tok = currentToken(inputEl);
    if (!tok || !labelState.active) return [];
    return tok.prefix === '@' ? priorityMatches(tok.partial) : labelMatches(tok.partial);
  });

  function openDropdown() {
    labelState = { active: true, index: 0 };
  }
  function closeDropdown() {
    labelState = { active: false, index: 0 };
  }

  function commitMatch(name) {
    const el = inputEl;
    const tok = currentToken(el);
    if (!tok) return false;
    const after = text.slice(tok.end);
    text = text.slice(0, tok.start) + name + ' ' + after;
    const caret = tok.start + name.length + 1;
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(caret, caret);
    });
    closeDropdown();
    return true;
  }

  function cycle(dir) {
    const n = suggestions.length;
    if (!n) return;
    labelState = { active: true, index: (labelState.index + dir + n) % n };
  }

  function onInput() {
    openDropdown();
  }

  async function submit(e) {
    e.preventDefault();
    const v = text.trim();
    if (!v) return;
    let res = null;
    try {
      res = await api.extractSchedule(v);
    } catch (e) {
      // Offline with text the worker never cached: queue the raw line. The
      // server re-extracts it at replay time, so the quick-add grammar
      // still applies (labels, priority, due date) once synced.
      if (e.status === undefined) {
        await onAdd?.({ title: v, rawText: v });
        text = '';
        preview = null;
        closeDropdown();
        return;
      }
      return; // server rejected the text (shown via preview): keep it for editing
    }
    if (!res || !res.ok || !res.title) return; // no usable title: keep the text
    const payload = { title: res.title, rawText: v };
    if (res.labels?.length) payload.labels = res.labels;
    if (res.priority) payload.priority = res.priority;
    if (res.dueDate) payload.dueDate = res.dueDate;
    if (res.recurrence) payload.recurrence = res.recurrence;
    await onAdd?.(payload);
    text = '';
    preview = null;
    closeDropdown();
  }

  function onKeydown(e) {
    const tok = currentToken(inputEl);
    const inToken = !!tok && labelState.active;
    const hasSug = suggestions.length > 0;

    if (e.key === 'Escape') {
      if (labelState.active) {
        closeDropdown();
        e.preventDefault();
        return;
      }
      text = '';
      onCancel ? onCancel() : e.target.blur();
      return;
    }

    if (inToken && hasSug) {
      if (e.key === 'Tab' && !e.altKey && !e.ctrlKey && !e.metaKey) {
        cycle(e.shiftKey ? -1 : 1);
        e.preventDefault();
        return;
      }
      if (e.key === 'ArrowDown') {
        cycle(1);
        e.preventDefault();
        return;
      }
      if (e.key === 'ArrowUp') {
        cycle(-1);
        e.preventDefault();
        return;
      }
      if (e.key === 'Enter') {
        commitMatch(suggestions[labelState.index]);
        e.preventDefault();
        return;
      }
    }

    // Open the dropdown when "#" or "!" is typed.
    if ((e.key === '#' || e.key === '@') && !e.ctrlKey && !e.metaKey && !e.altKey) {
      requestAnimationFrame(openDropdown);
    }
  }

  function onSuggestionClick(name) {
    commitMatch(name);
  }
</script>

<form class="row" onsubmit={submit}>
  <div class="field">
    <input
      type="text"
      bind:this={inputEl}
      bind:value={text}
      {placeholder}
      onkeydown={onKeydown}
      oninput={onInput}
      onblur={() => setTimeout(closeDropdown, 120)}
      use:focus
    />
    {#if suggestions.length}
      <ul class="popup" role="listbox">
        {#each suggestions as name, i (name)}
          <li
            role="option"
            aria-selected={i === labelState.index}
            class:active={i === labelState.index}
            onmousedown={(e) => { e.preventDefault(); onSuggestionClick(name); }}
            onmouseenter={() => (labelState = { ...labelState, index: i })}
          >
            <span class="hash">{currentToken(inputEl)?.prefix ?? '#'}</span>{name}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
  <button type="submit" class="primary action-btn" disabled={!canSubmit}>Add</button>
  {#if shownFeedback}
    <span class="preview">{shownFeedback}</span>
  {/if}
</form>

<style>
  .row {
    display: flex;
    gap: 8px;
    padding: 12px 20px;
    align-items: center;
  }
  .field {
    position: relative;
    flex: 1;
  }
  .popup {
    position: absolute;
    left: 0;
    top: 100%;
    z-index: 50;
    margin: 2px 0 0;
    padding: 4px;
    list-style: none;
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 6px;
    box-shadow: 0 4px 14px var(--shadow);
    max-height: 220px;
    overflow-y: auto;
    min-width: 140px;
    font-size: 13px;
  }
  .popup li {
    padding: 4px 8px;
    border-radius: 4px;
    cursor: pointer;
    white-space: nowrap;
  }
  .popup li.active {
    background: var(--accent);
    color: #fff;
  }
  .popup li .hash {
    opacity: 0.6;
    margin-right: 2px;
  }
  .popup li.active .hash {
    opacity: 0.8;
  }
  .preview {
    font-size: 12px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Mobile: the quick-add grammar preview moves to its own line so it can't
    squeeze the input; side padding matches the tighter mobile gutters. */
  @media (max-width: 720px) {
    .row {
      padding: 12px 12px;
      flex-wrap: wrap;
    }
    .preview {
      flex-basis: 100%;
      min-width: 0;
    }
  }
</style>
