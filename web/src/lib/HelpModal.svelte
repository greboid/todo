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

<div class="modal" role="dialog" aria-label="Syntax help" tabindex="-1" use:focus onkeydown={onKeydown}>
  <div class="modal-header">
    <h2><Icon name="help" size={18} /> Syntax help</h2>
    <button class="ghost close-btn" onclick={() => onClose?.()} aria-label="Close">
      <Icon name="close" size={18} />
    </button>
  </div>

  <section>
    <h3>Filter syntax</h3>
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
  </section>

  <section>
    <h3>Dates &amp; repeats</h3>
    <p class="hint">
      The due-date field takes a date, a repeat rule, or both (e.g.
      <code>every month on the 15th starting sep 1</code>). The same grammar works at the
      end of a quick-add line.
    </p>
    <dl>
      <dt><code>today</code> <code>tomorrow</code> <code>yesterday</code></dt>
      <dd>relative days</dd>
      <dt><code>fri</code> <code>this mon</code> <code>next tue</code></dt>
      <dd>coming weekday; <code>next</code> means next week's (weeks run Mon&ndash;Sun)</dd>
      <dt><code>aug 15</code> <code>15 aug</code> <code>aug 15 2027</code></dt>
      <dd>month + day; a past date without a year rolls to next year</dd>
      <dt><code>15th</code> <code>2026-08-15</code> <code>15/8/26</code></dt>
      <dd>day of this/next month; ISO date; day/month/year</dd>
      <dt><code>in 3 weeks</code> <code>+5</code></dt>
      <dd>offset from today in days, weeks, months, or years</dd>
      <dt><code>end of week</code> <code>end of month</code> <code>end of aug</code> <code>last day of month</code></dt>
      <dd>also <code>next week</code>/<code>month</code>/<code>year</code>, <code>this weekend</code>, <code>mid jun</code></dd>
      <dt><code>every day</code> <code>every week</code> <code>every month</code> <code>every year</code></dt>
      <dd>repeat on an interval; prefix a number for the step (<code>every 2 weeks</code>,
        <code>every other week</code>)</dd>
      <dt><code>every week on mon, wed</code></dt>
      <dd>repeat on specific weekdays</dd>
      <dt><code>every month on the 15th</code> <code>every month on the last day</code> <code>every last friday</code></dt>
      <dd>monthly targets: day(s) of month, last day, or an ordinal weekday (first&hellip;last)</dd>
      <dt><code>daily</code> <code>weekdays</code> <code>weekends</code> <code>quarterly</code> <code>fortnight</code></dt>
      <dd>repeat shorthands</dd>
      <dt><code>every! week</code></dt>
      <dd><code>!</code> advances from the completion date instead of the due date</dd>
      <dt><code>starting sep 1</code> <code>ending dec 31</code> <code>for 6 weeks</code></dt>
      <dd>recurrence window; without a start date the first due date is the rule's
        first occurrence</dd>
    </dl>
    <p class="hint">
      Completing a repeating todo spawns the next occurrence. Times (<code>at 3pm</code>,
      <code>every hour</code>) are not supported; the app is date-only.
    </p>
  </section>
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
  section {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  section + section {
    border-top: 1px solid var(--line);
    padding-top: 12px;
  }
  h3 {
    margin: 0;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
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
