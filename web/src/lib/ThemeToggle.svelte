<script>
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';

  // Three-way color theme preference: light / system / dark (system in the
  // middle and the default). Persisted in localStorage under 'theme'. The
  // app.css palette is plain CSS variables, so applying the choice is just a
  // matter of toggling a `theme-light`/`theme-dark` class on the root element;
  // with neither class the `prefers-color-scheme` media query follows the OS.
  const KEY = 'theme';
  const options = [
    { value: 'light', icon: 'sun', label: 'Light' },
    { value: 'system', icon: 'monitor', label: 'System' },
    { value: 'dark', icon: 'moon', label: 'Dark' },
  ];

  let theme = $state('system');

  function apply(value) {
    const root = document.documentElement;
    root.classList.toggle('theme-light', value === 'light');
    root.classList.toggle('theme-dark', value === 'dark');
  }

  onMount(() => {
    try {
      const saved = localStorage.getItem(KEY);
      if (saved === 'light' || saved === 'dark' || saved === 'system') theme = saved;
    } catch {
      /* storage unavailable (private mode etc.) — stay on the default */
    }
    apply(theme);
  });

  function select(value) {
    theme = value;
    try {
      localStorage.setItem(KEY, value);
    } catch {
      /* storage unavailable — preference is session-only */
    }
    apply(value);
  }
</script>

<div class="theme-toggle" role="group" aria-label="Color theme">
  {#each options as opt (opt.value)}
    <button
      class="seg"
      class:active={theme === opt.value}
      onclick={() => select(opt.value)}
      title={`${opt.label} theme`}
      aria-label={`${opt.label} theme`}
      aria-pressed={theme === opt.value}
    >
      <Icon name={opt.icon} size={14} />
    </button>
  {/each}
</div>

<style>
  .theme-toggle {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    padding: 2px;
    margin-right: 4px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--raised);
  }
  .seg {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 22px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
  }
  .seg:hover {
    color: var(--text);
  }
  .seg.active {
    background: var(--accent-tint);
    color: var(--accent-strong);
  }
</style>
