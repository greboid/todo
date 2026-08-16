<script>
  // Header badge reflecting the offline/sync machinery: Offline · N, Queued · N,
  // Syncing…, or Needs review when a merge decision is blocking the queue.
  // Clicking retries the flush (and reopens the merge dialog when one is
  // deferred). Reads all state from the offline module directly.
  import { offline } from './offline.svelte.js';

  let state = $derived.by(() => {
    if (offline.needsReview) return 'review';
    if (offline.syncing) return 'syncing';
    if (!offline.online) return 'offline';
    if (offline.queued.length > 0) return 'queued';
    if (offline.report) return 'notes';
    return null;
  });

  let label = $derived.by(() => {
    switch (state) {
      case 'review':
        return 'Needs review';
      case 'syncing':
        return 'Syncing…';
      case 'offline':
        return offline.queued.length > 0 ? `Offline · ${offline.queued.length}` : 'Offline';
      case 'queued':
        return `Queued · ${offline.queued.length}`;
      case 'notes':
        return 'Not synced';
      default:
        return '';
    }
  });

  let title = $derived.by(() => {
    switch (state) {
      case 'review':
        return 'Some offline changes clash with the server. Click to review.';
      case 'syncing':
        return 'Sending queued changes to the server…';
      case 'offline':
        return offline.queued.length > 0
          ? `Offline. ${offline.queued.length} change(s) saved locally and will sync when back online.`
          : 'Offline. Changes will be saved locally and synced when back online.';
      case 'queued':
        return `${offline.queued.length} change(s) waiting to sync.`;
      case 'notes':
        return offline.report || '';
      default:
        return '';
    }
  });

  function onclick() {
    if (offline.conflict || offline.queued.length > 0 || offline.needsReview) {
      offline.flushNow();
    } else {
      // Nothing left to sync: the click just acknowledges the notice.
      offline.acknowledgeReport();
    }
  }
</script>

{#if state}
  <button class="ghost tool-btn sync {state}" {onclick} {title} aria-live="polite">
    <span class="dot" aria-hidden="true"></span>{label}
  </button>
{/if}

<style>
  .sync {
    font-size: 12px;
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--muted);
    flex: none;
  }
  .offline .dot {
    background: var(--danger);
  }
  .offline {
    color: var(--danger);
  }
  .queued .dot {
    background: var(--recur);
  }
  .syncing .dot {
    background: var(--accent);
    animation: pulse 1s ease-in-out infinite;
  }
  .review .dot,
  .notes .dot {
    background: var(--danger);
    animation: pulse 1.5s ease-in-out infinite;
  }
  .review,
  .notes {
    color: var(--danger);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
</style>
