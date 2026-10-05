import { expect, test } from '@playwright/test';
import type { Response } from '@playwright/test';

// Smoke della UI attraverso l'Ingress (GIT-152). Variabili d'ambiente:
//   E2E_BASE_URL            indirizzo dell'Ingress (letto da playwright.config.ts)
//   E2E_ADMIN_PASSWORD      password corrente dell'admin (CI: dal Secret gitstack-identity-admin)
//   E2E_ADMIN_NEW_PASSWORD  serve solo se l'admin ha ancora la password iniziale:
//                           la UI impone il cambio (>= 12 caratteri) e lo smoke lo esegue.
//                           ATTENZIONE: su una VM questo cambia davvero la password.
const username = process.env.E2E_ADMIN_USERNAME ?? 'admin';
const password = process.env.E2E_ADMIN_PASSWORD ?? '';
const newPassword = process.env.E2E_ADMIN_NEW_PASSWORD ?? '';

test('login admin, pagina principale, lista repo senza 4xx/5xx su /api', async ({ page }) => {
  expect(password, 'E2E_ADMIN_PASSWORD mancante').not.toBe('');

  const bad: string[] = [];
  let loggedIn = false;
  page.on('response', (res: Response) => {
    const url = new URL(res.url());
    if (!url.pathname.startsWith('/api')) return;
    const status = res.status();
    if (status < 400) return;
    // 401 atteso solo prima del login (controllo di sessione su /login).
    if (status === 401 && !loggedIn) return;
    bad.push(`${res.request().method()} ${url.pathname} -> ${status}`);
  });

  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Sign in to GitStack' })).toBeVisible();
  await page.getByLabel('Username or email').fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  const loginResponse = page.waitForResponse((r) => r.url().includes('/auth/login') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Sign in' }).click();
  expect((await loginResponse).status(), 'POST /auth/login').toBe(200);
  loggedIn = true;

  // Password iniziale: la UI porta a /change-password prima di tutto il resto.
  if (/\/change-password$/.test(page.url()) || (await page.getByRole('heading', { name: 'Change your password' }).isVisible().catch(() => false))) {
    expect(newPassword.length, 'password iniziale: serve E2E_ADMIN_NEW_PASSWORD (>= 12 caratteri)').toBeGreaterThanOrEqual(12);
    await page.getByLabel('Current password', { exact: true }).fill(password);
    await page.getByLabel('New password', { exact: true }).fill(newPassword);
    await page.getByLabel('Confirm new password', { exact: true }).fill(newPassword);
    await page.getByRole('button', { name: 'Change password' }).click();
  }

  // Pagina principale (Resources) dentro l'AppShell.
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('link', { name: 'Repositories' }).first()).toBeVisible();

  // Lista dei repo.
  await page.getByRole('link', { name: 'Repositories' }).first().click();
  await expect(page).toHaveURL(/\/repos$/);
  await expect(page.getByRole('heading', { name: 'Repositories', level: 1 })).toBeVisible();
  await page.waitForLoadState('networkidle');

  expect(bad, `risposte /api 4xx/5xx:\n${bad.join('\n')}`).toEqual([]);
});
