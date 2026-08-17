// The todos-list cache key strips ?today= (it changes at midnight and would
// fork the cache daily) and the cached response is stamped with the day it
// was computed for. Reopening the app offline on a later date must show the
// cached list badged as an outdated view instead of a blank list — and clear
// the badge once a fresh load lands. Same-day offline reopens stay unbadged.
import { expect, test } from './fixtures.js';
import { addTodo, badgeText, nudgeSync, row } from './helpers.js';

// Advance the fake clock so any debounced load (sync pokes, reconnect
// catch-ups) scheduled under it actually fires; a no-op if time flows.
const settleClock = (page) => page.clock.fastForward(2000);

test('offline reopen on a later date shows the cached list as outdated', async ({ page, server }) => {
  const DAY1 = '2026-01-10';
  const DAY2 = '2026-01-11'; // DAY1 08:00 + 30h, so no midnight edge cases

  // Day one, online: pin the clock so ?today= is deterministic and seed the
  // cache with the list that later days must fall back to.
  await page.clock.install({ time: `${DAY1}T08:00:00` });
  await page.goto(`${server.base}/?filter=`);
  await addTodo(page, 'Cached on day one');
  await expect(row(page, 'Cached on day one')).toBeVisible();
  await expect(page.locator('.stale-view')).toHaveCount(0);

  // Same-day offline reopen: cache-served, but stamped with today — no badge.
  await server.stop();
  await page.reload();
  await expect(row(page, 'Cached on day one')).toBeVisible();
  await expect(page.locator('.stale-view')).toHaveCount(0);
  await server.start();
  await nudgeSync(page);
  await settleClock(page);
  await expect(row(page, 'Cached on day one')).toBeVisible();

  // A new day, offline: the stripped-`today` key still hits and the stamped
  // day differs from the device's, so the list renders with the badge.
  await page.clock.fastForward('30:00:00');
  await server.stop();
  await page.reload();
  await expect(row(page, 'Cached on day one')).toBeVisible();
  await expect(page.locator('.stale-view')).toHaveText(/Cached view/);
  await expect.poll(() => badgeText(page)).toContain('Offline');

  // Back online on day two: the fresh list replaces the view and the badge
  // goes away on its own.
  await server.start();
  await nudgeSync(page);
  await settleClock(page);
  await expect(page.locator('.stale-view')).toHaveCount(0);
  await expect(row(page, 'Cached on day one')).toBeVisible();
});
