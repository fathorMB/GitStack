import { expect, test } from '@playwright/test';

// Run with a local Vite server: E2E_BASE_URL=http://127.0.0.1:5173 pnpm e2e log-layout.spec.ts
for (const width of [1280, 390]) {
  test(`run log wraps without page overflow at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.route('**/api/v1/**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.includes('/commits/') && path.endsWith('/status')) return route.fulfill({ status: 404, json: { error: { message: 'not found' } } });
      let data: unknown = { items: [] };
      if (path.endsWith('/auth/session')) data = { user: { username: 'alice', isAdmin: false } };
      else if (path.endsWith('/repos/acme/api')) data = { id: 'r1', owner: { type: 'organization', name: 'acme' }, name: 'api', archived: false, defaultBranch: 'main', visibility: 'private' };
      else if (path.endsWith('/actions/runs/run1')) data = {
        id: 'run1', workflowId: 'wf1', status: 'in_progress', conclusion: null, event: 'push', ref: 'refs/heads/main', sha: 'a91c3e2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', createdAt: '2026-10-19T10:00:00Z', startedAt: '2026-10-19T10:00:00Z', completedAt: null,
        jobs: [{ id: 'j1', name: 'test', status: 'in_progress', conclusion: null, runnerType: 'internal_pod', permissions: {}, startedAt: '2026-10-19T10:00:00Z', completedAt: null }],
      };
      else if (path.endsWith('/actions/jobs/j1/logs')) return route.fulfill({ contentType: 'text/plain', body: 'x'.repeat(500) });
      await route.fulfill({ json: data });
    });
    await page.goto('/acme/api/actions/runs/run1');
    const line = page.locator('[role="log"] .l');
    await expect(line).toHaveText('1' + 'x'.repeat(500));
    const metrics = await line.evaluate((el) => {
      const text = el.querySelector('.log-text')!.firstChild as Text;
      const fragments = Array.from({ length: text.length }, (_, i) => {
        const range = document.createRange(); range.setStart(text, i); range.setEnd(text, i + 1);
        const rect = range.getBoundingClientRect(); return { x: rect.x, y: rect.y, right: rect.right };
      });
      const rows = [...new Map(fragments.map((r) => [r.y, r])).values()];
      const firsts = rows.map((row) => fragments.find((r) => r.y === row.y)!);
      const numberRange = document.createRange(); numberRange.selectNodeContents(el.querySelector('.n')!);
      const number = numberRange.getBoundingClientRect();
      const log = el.closest('.log')!.getBoundingClientRect();
      return { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth, rows: rows.length, starts: firsts.map((r) => r.x), numberTop: number.top, firstTop: firsts[0]!.y, numberCount: el.querySelectorAll('.n').length, textLeft: Math.min(...fragments.map((r) => r.x)), textRight: Math.max(...fragments.map((r) => r.right)), logLeft: log.left, logRight: log.right };
    });
    console.log(`${width}px: ${JSON.stringify(metrics)}`);
    expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth);
    expect(metrics.rows).toBeGreaterThan(1);
    expect(metrics.numberCount).toBe(1);
    expect(Math.abs(metrics.numberTop - metrics.firstTop)).toBeLessThan(3);
    for (const start of metrics.starts) expect(Math.abs(start - metrics.starts[0]!)).toBeLessThan(1);
    expect(metrics.textLeft).toBeGreaterThanOrEqual(metrics.logLeft);
    expect(metrics.textRight).toBeLessThanOrEqual(metrics.logRight);
  });
}
