import { expect, test } from '@playwright/test';

// Richiede database abilitati e un'app pronta dello stesso owner: E2E_BASE_URL,
// E2E_ADMIN_PASSWORD, E2E_DATABASE_APP_REPO e E2E_DATABASE_APP_NAME.
// E2E_ADMIN_USERNAME è facoltativa (default: admin).
test.skip(!process.env.E2E_DATABASE_APP_REPO || !process.env.E2E_DATABASE_APP_NAME, "richiede un'istanza con database abilitati: lo lancia il passo e2e su VM di GIT-558");

test('gestione database dal portale', async ({ page }) => {
  test.setTimeout(600_000);
  const owner = process.env.E2E_ADMIN_USERNAME ?? 'admin';
  const password = process.env.E2E_ADMIN_PASSWORD ?? '';
  const repo = process.env.E2E_DATABASE_APP_REPO ?? '';
  const app = process.env.E2E_DATABASE_APP_NAME ?? '';
  expect(password, 'E2E_ADMIN_PASSWORD richiesta').not.toBe('');
  const name = `e2e-${Date.now()}`;

  await page.goto('/login');
  await page.getByLabel('Username or email').fill(owner);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.waitForURL(/\/$/);
  await page.goto('/databases/new');
  await page.getByLabel('Name', { exact: true }).fill(name);
  await page.getByRole('button', { name: 'Create database' }).click();
  await page.getByRole('link', { name: 'Open database' }).click();
  await expect.poll(async () => {
    await page.getByRole('button', { name: 'Refresh' }).click();
    return page.getByText('Status: running').count();
  }, { timeout: 300_000, intervals: [5000] }).toBe(1);

  await page.goto(`/${owner}/${repo}/apps/${app}`);
  await page.getByLabel('Database', { exact: true }).selectOption(name);
  await page.getByRole('button', { name: 'Link database' }).click();
  await expect(page.getByRole('link', { name })).toBeVisible();
  await page.getByRole('link', { name }).click();
  await page.getByRole('button', { name: 'Create backup' }).click();
  await expect.poll(async () => {
    await page.reload();
    return page.getByText(/on_demand · completed/).count();
  }, { timeout: 300_000, intervals: [5000] }).toBeGreaterThan(0);
  await page.getByLabel('Restore confirmation').fill(`${owner}/${name}`);
  await page.getByRole('button', { name: 'Restore' }).first().click();
  await expect.poll(async () => {
    await page.getByRole('button', { name: 'Refresh' }).click();
    return page.getByText('Status: running').count();
  }, { timeout: 300_000, intervals: [5000] }).toBe(1);
  await page.getByRole('button', { name: 'Enable LAN access' }).click();
  await page.getByRole('button', { name: 'Disable LAN access' }).click();
  await page.getByLabel(`Type ${owner}/${name} to delete`).fill(`${owner}/${name}`);
  await page.getByRole('button', { name: 'Delete database' }).click();
  await expect(page).toHaveURL(/\/databases$/);
});
