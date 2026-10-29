'use strict';

// The host installs the verified CA temporarily before starting this browser.
// Require Playwright from web/, whose installation is documented in the VM README.
const path = require('node:path');
const { chromium } = require(path.resolve(__dirname, '../../../web/node_modules/@playwright/test'));

function describe(response, page) {
  return 'status ' + (response ? response.status() : 'nessuna risposta') + ', url ' + page.url();
}

// Chromium ignores the Accept of extraHTTPHeaders on navigations, so the header
// is rewritten per request with a route. context.request is not usable here:
// it runs in Node and does not see --host-resolver-rules.
async function checkAnonymousBlocked(browser, href) {
  const anonymous = await browser.newContext();
  try {
    await anonymous.route('**/*', route => route.continue({
      headers: { ...route.request().headers(), accept: 'application/json' }
    }));
    const anonymousPage = await anonymous.newPage();
    const denied = await anonymousPage.goto(href, { waitUntil: 'domcontentloaded', timeout: 60000 });
    if (!denied || denied.status() !== 401) {
      throw new Error('anonymous request was not denied with 401: ' + describe(denied, anonymousPage));
    }
  } finally {
    await anonymous.close();
  }
  console.log('STEP ok anonymous-blocked');
}

async function main() {
  const address = new URL(process.env.PILOT_SITE_URL);
  const ip = process.env.PILOT_VM_IP;
  const token = process.env.PILOT_TESTER_TOKEN;
  const screenshot = process.env.PILOT_SCREENSHOT;
  if (address.protocol !== 'https:' || !ip || !token || !screenshot) throw new Error('missing pilot browser configuration');
  const browser = await chromium.launch({ headless: true, args: [
    '--no-proxy-server', '--host-resolver-rules=MAP ' + address.hostname + ' ' + ip
  ] });
  try {
    await checkAnonymousBlocked(browser, address.href);
    const context = await browser.newContext({ extraHTTPHeaders: { Authorization: 'Bearer ' + token } });
    const page = await context.newPage();
    const response = await page.goto(address.href, { waitUntil: 'networkidle', timeout: 60000 });
    if (!response || response.status() !== 200) throw new Error('site HTTPS response is not 200: ' + describe(response, page));
    await page.locator('#data').waitFor({ state: 'visible', timeout: 30000 });
    await page.waitForFunction(() => document.querySelector('#data').textContent === 'retail-pilot-381', null, { timeout: 60000 });
    await page.screenshot({ path: screenshot });
    console.log('STEP ok tester-https-data');
    await page.locator('#screenshot').setInputFiles(screenshot);
    await page.locator('#send').click();
    await page.waitForFunction(() => document.querySelector('#result').textContent.length > 0, null, { timeout: 60000 });
    const result = JSON.parse(await page.locator('#result').textContent());
    if (!Number.isSafeInteger(result.number) || result.number < 1) throw new Error('site did not return an issue number');
    console.log('ISSUE=' + result.number);
    console.log('STEP ok tester-report');
    await context.close();
  } finally {
    await browser.close();
  }
}

module.exports = { checkAnonymousBlocked };

if (require.main === module) {
  main().catch(error => { console.error('STEP FAIL browser: ' + error.message); process.exitCode = 1; });
}
