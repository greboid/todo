<script>
  // Simple merge dialog, shown while the offline flush is paused on a clash:
  // the server's current version of a todo differs from what a queued
  // offline change was based on (another device edited it while we were
  // offline, or deleted it). One conflict at a time — the flush raises the
  // next one, if any, after this is resolved.
  import { offline } from './offline.svelte.js';
  import Icon from './Icon.svelte';
  import { focus } from './actions.js';

  let c = $derived(offline.conflict);

  function onKeydown(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      offline.deferConflict();
    }
  }
</script>

{#if c}
  <div class="backdrop"></div>

  <div class="modal" role="dialog" aria-label="Resolve conflicting changes" tabindex="-1" use:focus onkeydown={onKeydown}>
    <div class="modal-header">
      <h2><Icon name="help" size={18} /> Conflicting changes</h2>
    </div>

    <p class="intro">
      {#if c.deletedOnServer}
        <strong>“{c.title}”</strong> was deleted on the server while you were offline, so
        {c.summary} can no longer be applied.
      {:else}
        <strong>“{c.title}”</strong> was changed on the server while you were offline
        ({c.summary} here). Choose which version to keep.
      {/if}
    </p>

    <table>
      <thead>
        <tr><th></th><th>This device</th><th>Server</th></tr>
      </thead>
      <tbody>
        {#each c.rows as row (row.label)}
          <tr>
            <th scope="row">{row.label}</th>
            <td>{row.mine}</td>
            <td>{row.theirs}</td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="row">
      {#if c.deletedOnServer}
        <button class="primary" onclick={() => offline.resolveConflict('theirs')}>Discard my change</button>
      {:else}
        <button class="primary" onclick={() => offline.resolveConflict('mine')}>Keep my changes</button>
        <button onclick={() => offline.resolveConflict('theirs')}>Keep server version</button>
      {/if}
      <button class="ghost" onclick={() => offline.deferConflict()}>Decide later</button>
    </div>
    <p class="hint">
      “Decide later” keeps the change queued (the badge shows <em>Needs review</em>) and syncs
      the rest.
    </p>
  </div>
{/if}

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    background: var(--shadow);
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
    width: min(440px, calc(100vw - 32px));
    max-height: calc(100vh - 64px);
    overflow-y: auto;
    overscroll-behavior: contain;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .modal-header h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-size: 16px;
  }
  .intro {
    margin: 0;
    font-size: 13px;
    line-height: 1.5;
    color: var(--text);
  }
  table {
    border-collapse: collapse;
    font-size: 13px;
    width: 100%;
  }
  th,
  td {
    border: 1px solid var(--line);
    padding: 6px 8px;
    text-align: left;
    vertical-align: top;
    overflow-wrap: anywhere;
  }
  thead th {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
    background: var(--raised);
  }
  tbody th {
    font-weight: 600;
    white-space: nowrap;
  }
  .row {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }
</style>
