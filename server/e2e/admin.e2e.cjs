// End-to-end check of the Admin UI: drives a real browser against a real server.
//
//   FLARE_SERVER_BIN=bin/flare node e2e/admin.e2e.cjs
//
// It starts the server on a free port with a throw-away database, makes an administrator and an
// ordinary user from the command line, then does through the page everything an administrator
// does, and checks each effect through the public API, not through the page. Needs Playwright
// (`npm i -g playwright`) and a Chromium it can start. Screenshots go to $FLARE_E2E_SHOTS if set.
'use strict';

const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { chromium } = require('playwright');

const BIN = path.resolve(process.env.FLARE_SERVER_BIN || path.join(__dirname, '..', 'bin', 'flare'));
const SHOTS = process.env.FLARE_E2E_SHOTS;
const ADMIN = { name: 'root', password: 'correct horse battery' };
const BOB = { name: 'bob', password: 'bob has a long password' };

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
    s.on('error', reject);
  });
}

async function startServer(dir) {
  const port = await freePort();
  const db = `sqlite://${path.join(dir, 'flare.db')}`;
  for (const [user, extra] of [[ADMIN, ['--admin']], [BOB, []]]) {
    const r = spawnSync(BIN, ['user', 'add', ...extra, '--database', db, user.name], { input: user.password + '\n', encoding: 'utf8' });
    assert.equal(r.status, 0, `could not add ${user.name}: ${r.stderr}${r.stdout}`);
  }
  const base = `http://127.0.0.1:${port}`;
  const child = spawn(BIN, ['serve', '--listen', `127.0.0.1:${port}`, '--database', db, '--base-url', base, '--log-level', 'warn', '--log-format', 'text'], { stdio: ['ignore', 'pipe', 'pipe'] });
  let log = '';
  child.stdout.on('data', (d) => (log += d));
  child.stderr.on('data', (d) => (log += d));
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return { base, child, log: () => log };
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  child.kill();
  throw new Error('the server did not start: ' + log);
}

const basic = (u) => 'Basic ' + Buffer.from(`${u.name}:${u.password}`).toString('base64');

async function main() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'flare-admin-e2e-'));
  const server = await startServer(dir);
  const { base } = server;
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
  const page = await context.newPage();

  const problems = [];
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`));
  page.on('console', (m) => {
    // The browser logs every failed request; the ones these tests cause on purpose are not problems.
    if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) problems.push(`console: ${m.text()}`);
  });
  await page.addInitScript(() => {
    window.__violations = [];
    document.addEventListener('securitypolicyviolation', (e) => window.__violations.push(`${e.violatedDirective} ${e.blockedURI}`));
  });

  let n = 0;
  const shot = async (name) => {
    if (SHOTS) await page.screenshot({ path: path.join(SHOTS, `${String(++n).padStart(2, '0')}-${name}.png`), fullPage: true });
  };
  const step = async (name, fn) => {
    try {
      await fn();
      console.log(`  ok   ${name}`);
    } catch (e) {
      console.log(`  FAIL ${name}`);
      await shot(`FAIL-${name.replace(/\W+/g, '-')}`).catch(() => {});
      throw e;
    }
  };
  const api = async (method, p, { user, token, body } = {}) => {
    const headers = { Accept: 'application/json' };
    if (user) headers.Authorization = basic(user);
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const r = await fetch(base + p, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await r.text();
    return { status: r.status, data: text ? JSON.parse(text) : null };
  };
  const toast = (text) => page.locator('#toasts .toast', { hasText: text }).first();
  const dialog = page.locator('dialog[open]');
  const setting = (label) => page.locator('.setting').filter({ has: page.getByLabel(label, { exact: true }) });
  const colour = (label) => page.getByLabel(`${label}: colour code`, { exact: true });

  try {
    await step('/admin redirects to the page, which asks for a sign-in', async () => {
      await page.goto(`${base}/admin`);
      assert.equal(new URL(page.url()).pathname, '/admin/');
      await page.getByRole('heading', { name: 'Flare admin' }).waitFor();
      assert.equal(await page.title(), 'Flare admin');
      await shot('sign-in');
    });

    await step('a wrong password is refused, with no hint which part was wrong', async () => {
      await page.getByLabel('User name').fill(ADMIN.name);
      await page.getByLabel('Password').fill('nope nope nope');
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByText('The user name or password is wrong.').waitFor();
    });

    await step('an ordinary user is turned away and leaves no token behind', async () => {
      await page.getByLabel('User name').fill(BOB.name);
      await page.getByLabel('Password').fill(BOB.password);
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByText('This account is not an administrator.').waitFor();
      const tokens = await api('GET', '/v1/account/tokens', { user: BOB });
      assert.equal(tokens.status, 200);
      assert.deepEqual(tokens.data.tokens, [], 'the refused sign-in must not leave a token');
    });

    await step('an administrator signs in', async () => {
      await page.getByLabel('User name').fill(ADMIN.name);
      await page.getByLabel('Password').fill(ADMIN.password);
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
      await page.getByText('No themes yet.').waitFor();
      await shot('themes-empty');
    });

    await step('the session survives a reload', async () => {
      await page.reload();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
    });

    await step('a theme is added, and the API then offers it to apps', async () => {
      await page.getByRole('button', { name: 'Add the first theme' }).click();
      await page.getByRole('heading', { name: 'New theme' }).waitFor();
      await page.getByLabel('Name', { exact: true }).fill('Midnight Test');
      await page.getByLabel('Brightness').selectOption('dark');
      await colour('Primary').fill('#8AB4F8');
      await page.getByRole('button', { name: 'Rebuild from the primary' }).click();
      await dialog.waitFor({ state: 'detached' }).catch(() => {}); // asks only if other colours were set
      await shot('theme-editor');
      await page.getByRole('button', { name: 'Add theme' }).click();
      await page.getByRole('heading', { name: 'Midnight Test' }).waitFor();
      const themes = await api('GET', '/v1/themes');
      const t = themes.data.themes.find((x) => x.name === 'Midnight Test');
      assert.ok(t, 'the theme must be on the server');
      assert.equal(t.brightness, 'dark');
      assert.equal(t.colors.primary, '#8AB4F8');
      assert.ok(Object.keys(t.colors).length > 20, 'a rebuilt palette sets every colour');
    });

    await step('text that cannot be read stops the save, and says which', async () => {
      await colour('Text on primary').fill('#8AB4F8');
      await page.getByText('The server will not accept this').waitFor();
      assert.equal(await page.getByRole('button', { name: 'Save changes' }).isDisabled(), true);
      await shot('theme-unreadable');
      await colour('Text on primary').fill('#000000');
      await page.getByText('The server will not accept this').waitFor({ state: 'hidden' });
    });

    await step('an edit is saved, and the stored theme changes', async () => {
      await colour('Primary').fill('#FFB74D');
      await page.getByRole('button', { name: 'Save changes' }).click();
      await toast('Saved').waitFor();
      const t = (await api('GET', '/v1/themes')).data.themes.find((x) => x.name === 'Midnight Test');
      assert.equal(t.colors.primary, '#FFB74D');
    });

    await step('leaving with unsaved changes asks first, and Cancel stays', async () => {
      await colour('Primary').fill('#112233');
      await page.locator('nav a', { hasText: 'Settings' }).click();
      await dialog.getByRole('heading', { name: 'Discard changes?' }).waitFor();
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      assert.match(page.url(), /#\/themes\/Midnight%20Test$/);
      assert.equal(await colour('Primary').inputValue(), '#112233');
      await page.locator('nav a', { hasText: 'Settings' }).click();
      await dialog.getByRole('button', { name: 'Discard' }).click();
      await page.getByRole('heading', { name: 'Settings', exact: true }).waitFor();
      const t = (await api('GET', '/v1/themes')).data.themes.find((x) => x.name === 'Midnight Test');
      assert.equal(t.colors.primary, '#FFB74D', 'discarded changes must not be saved');
    });

    await step('a name that is taken, in any case, is refused before anything is sent', async () => {
      await page.locator('nav a', { hasText: 'Themes' }).click();
      await page.getByRole('button', { name: 'New theme' }).click();
      await page.getByLabel('Name', { exact: true }).fill('midnight test');
      await page.getByText('A theme with this name exists').waitFor();
      assert.equal(await page.getByRole('button', { name: 'Add theme' }).isDisabled(), true);
      await page.getByLabel('Name', { exact: true }).fill('bad/name');
      await page.getByText('Use 1 to 32 letters').waitFor();
      // The new theme has been changed, so leaving it asks.
      await page.locator('nav a', { hasText: 'Themes' }).click();
      await dialog.getByRole('button', { name: 'Discard' }).click();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
    });

    await step('a theme is duplicated, exported and imported', async () => {
      await page.getByRole('link', { name: /Midnight Test/ }).click();
      await page.getByRole('button', { name: 'Duplicate' }).click();
      await dialog.getByLabel('Name of the copy').fill('Night Copy');
      await dialog.getByRole('button', { name: 'Duplicate' }).click();
      await page.getByRole('heading', { name: 'Night Copy' }).waitFor();
      const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'Export' }).click()]);
      assert.equal(download.suggestedFilename(), 'Night Copy.theme.json');
      const exported = JSON.parse(fs.readFileSync(await download.path(), 'utf8'));
      assert.equal(exported.name, 'Night Copy');
      assert.equal(exported.colors.primary, '#FFB74D');

      await page.locator('nav a', { hasText: 'Themes' }).click();
      await page.getByRole('button', { name: 'Import…' }).click();
      exported.name = 'Imported';
      exported.brightness = 'light';
      await dialog.getByLabel('Theme JSON').fill('{not json');
      await dialog.getByRole('button', { name: 'Open in the editor' }).click();
      await dialog.getByText('That is not valid JSON.').waitFor();
      await dialog.getByLabel('Theme JSON').fill(JSON.stringify(exported));
      await dialog.getByRole('button', { name: 'Open in the editor' }).click();
      await page.getByRole('heading', { name: 'New theme' }).waitFor();
      assert.equal(await page.getByLabel('Name', { exact: true }).inputValue(), 'Imported');
      assert.equal(await colour('Primary').inputValue(), '#FFB74D');
      await page.getByRole('button', { name: 'Add theme' }).click();
      await page.getByRole('heading', { name: 'Imported' }).waitFor();
      const names = (await api('GET', '/v1/themes')).data.themes.map((t) => t.name).sort();
      assert.deepEqual(names, ['Imported', 'Midnight Test', 'Night Copy']);
      await page.locator('nav a', { hasText: 'Themes' }).click();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
      await shot('themes-list');
    });

    await step('a theme is deleted after confirming', async () => {
      await page.getByRole('link', { name: /Imported/ }).click();
      await page.getByRole('button', { name: 'Delete' }).click();
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      assert.equal((await api('GET', '/v1/themes')).data.themes.length, 3);
      await page.getByRole('button', { name: 'Delete' }).click();
      await dialog.getByRole('button', { name: 'Delete' }).click();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
      assert.equal((await api('GET', '/v1/themes')).data.themes.length, 2);
    });

    await step('a setting changes at once, and can be reset', async () => {
      await page.locator('nav a', { hasText: 'Settings' }).click();
      await page.getByRole('heading', { name: 'Settings', exact: true }).waitFor();
      await shot('settings');
      const name = setting('Server name');
      await page.getByLabel('Server name', { exact: true }).fill('Acme Alerts');
      await name.getByRole('button', { name: 'Save' }).click();
      await toast('Server name saved.').waitFor();
      assert.equal((await api('GET', '/v1/config')).data.name, 'Acme Alerts');
      assert.equal(await page.title(), 'Acme Alerts admin');
      await page.getByText('The configuration says: Flare').waitFor();
      await setting('Server name').getByRole('button', { name: 'Reset' }).click();
      await dialog.getByRole('button', { name: 'Reset' }).click();
      await toast('Server name reset.').waitFor();
      assert.equal((await api('GET', '/v1/config')).data.name, 'Flare');
    });

    await step('a setting that is not valid is not sent', async () => {
      await page.getByLabel('Accent colour', { exact: true }).fill('orange');
      await page.getByText('Enter a colour like #F97316.').waitFor();
      assert.equal(await setting('Accent colour').getByRole('button', { name: 'Save' }).isDisabled(), true);
      await page.getByLabel('Accent colour', { exact: true }).fill('#336699');
      await setting('Accent colour').getByRole('button', { name: 'Save' }).click();
      await toast('Accent colour saved.').waitFor();
      assert.equal((await api('GET', '/v1/config')).data.primary_color, '#336699');
      await setting('Accent colour').getByRole('button', { name: 'Reset' }).click();
      await dialog.getByRole('button', { name: 'Reset' }).click();
      await toast('Accent colour reset.').waitFor();
    });

    await step('a length of time is shown in the largest exact unit and saved in seconds', async () => {
      const units = page.getByLabel('How long messages are kept: unit');
      const number = page.getByLabel('How long messages are kept', { exact: true });
      assert.equal(await units.inputValue(), '86400', 'the default of a day is shown in days');
      assert.equal(await number.inputValue(), '1');
      await number.fill('36');
      await units.selectOption('3600');
      await setting('How long messages are kept').getByRole('button', { name: 'Save' }).click();
      await toast('How long messages are kept saved.').waitFor();
      const item = (await api('GET', '/v1/admin/settings', { user: ADMIN })).data.settings.find((s) => s.key === 'cache_duration');
      assert.equal(item.value, 129600);
      assert.equal(item.overridden, true);
      // 36 hours is not a whole number of days, so it stays in hours.
      assert.equal(await units.inputValue(), '3600');
      assert.equal(await number.inputValue(), '36');
      await number.fill('0');
      await page.getByText('Enter a length of time from').waitFor();
    });

    await step('a setting that needs a restart says so', async () => {
      const row = setting('Keep message text out of push services');
      const before = (await api('GET', '/v1/admin/settings', { user: ADMIN })).data.settings.find((s) => s.key === 'push.content_free').value;
      await page.getByLabel('Keep message text out of push services', { exact: true }).setChecked(!before);
      await row.getByRole('button', { name: 'Save' }).click();
      await toast('applies after the server restarts').waitFor();
      await page.getByText('Waiting for a restart: Keep message text out of push services').waitFor();
      await shot('settings-restart');
    });

    await step('a user is added, disabled, enabled, given a new password and removed', async () => {
      await page.locator('nav a', { hasText: 'Users' }).click();
      await page.getByRole('heading', { name: 'Users', exact: true }).waitFor();
      const me = page.locator('tr', { hasText: ADMIN.name });
      assert.equal(await me.getByRole('button', { name: 'Disable' }).count(), 0, 'you cannot disable yourself');
      assert.equal(await me.getByRole('button', { name: 'Delete' }).count(), 0, 'you cannot delete yourself');
      assert.equal(await me.getByLabel(`Role of ${ADMIN.name}`).isDisabled(), true);

      await page.getByRole('button', { name: 'Add user' }).click();
      await dialog.getByLabel('Name').fill('carol');
      await dialog.getByLabel('Password').fill('short');
      await dialog.getByRole('button', { name: 'Add user' }).click();
      await dialog.locator('.error-text').waitFor();
      await dialog.getByLabel('Password').fill('carol has a long password');
      await dialog.getByRole('button', { name: 'Add user' }).click();
      await toast('carol is added.').waitFor();
      const carol = { name: 'carol', password: 'carol has a long password' };
      assert.equal((await api('POST', '/v1/auth/login', { user: carol, body: {} })).status, 200);

      const row = page.locator('tr', { hasText: 'carol' });
      await row.getByRole('button', { name: 'Disable' }).click();
      await dialog.getByRole('button', { name: 'Disable' }).click();
      await toast('carol is disabled.').waitFor();
      assert.equal((await api('POST', '/v1/auth/login', { user: carol, body: {} })).status, 401, 'a disabled user cannot sign in');
      await page.locator('tr', { hasText: 'carol' }).getByRole('button', { name: 'Enable' }).click();
      await toast('carol is enabled.').waitFor();
      assert.equal((await api('POST', '/v1/auth/login', { user: carol, body: {} })).status, 200);

      await page.locator('tr', { hasText: 'carol' }).getByRole('button', { name: 'Set password' }).click();
      await dialog.getByLabel('Password').fill('a brand new password');
      await dialog.getByRole('button', { name: 'Change password' }).click();
      await toast('Password of carol changed.').waitFor();
      assert.equal((await api('POST', '/v1/auth/login', { user: carol, body: {} })).status, 401, 'the old password stops working');
      carol.password = 'a brand new password';

      await page.locator('tr', { hasText: 'carol' }).getByLabel('Role of carol').selectOption('admin');
      await toast('carol is now an administrator.').waitFor();
      assert.equal((await api('GET', '/v1/admin/users', { user: carol })).status, 200);
      await shot('users');

      await page.locator('tr', { hasText: 'carol' }).getByRole('button', { name: 'Tokens' }).click();
      await dialog.getByRole('heading', { name: 'Tokens of carol' }).waitFor();
      assert.ok(await dialog.locator('tbody tr').count() >= 1, 'carol has tokens from the logins above');
      const before = await dialog.locator('tbody tr').count();
      await dialog.locator('tbody tr').first().getByRole('button', { name: 'Revoke' }).click();
      await toast('Token revoked.').waitFor();
      await page.waitForFunction((b) => document.querySelectorAll('dialog[open] tbody tr').length < b || !!document.querySelector('dialog[open] .help'), before);
      await dialog.getByRole('button', { name: 'Close' }).click();

      await page.locator('tr', { hasText: 'carol' }).getByRole('button', { name: 'Delete' }).click();
      await dialog.getByRole('button', { name: 'Delete' }).click();
      await toast('carol is deleted.').waitFor();
      assert.equal((await api('POST', '/v1/auth/login', { user: carol, body: {} })).status, 401);
    });

    await step('an access rule is added, changed and removed', async () => {
      await page.locator('nav a', { hasText: 'Access' }).click();
      await page.getByRole('heading', { name: 'Access', exact: true }).waitFor();
      await page.getByRole('button', { name: 'Add rule' }).click();
      await dialog.getByLabel('Topics').fill('news-*');
      await dialog.getByLabel('Access').selectOption('read-only');
      await dialog.getByRole('button', { name: 'Save rule' }).click();
      await toast('Rule saved.').waitFor();
      let rules = (await api('GET', '/v1/admin/access', { user: ADMIN })).data.rules;
      assert.deepEqual(rules.map((r) => [r.who, r.pattern, r.access]), [['everyone', 'news-*', 'read-only']]);
      await page.locator('tr', { hasText: 'news-*' }).getByRole('button', { name: 'Change' }).click();
      await dialog.getByLabel('Access').selectOption('deny-all');
      await dialog.getByRole('button', { name: 'Save rule' }).click();
      await toast('Rule saved.').waitFor();
      rules = (await api('GET', '/v1/admin/access', { user: ADMIN })).data.rules;
      assert.equal(rules[0].access, 'deny-all');
      await shot('access');
      await page.locator('tr', { hasText: 'news-*' }).getByRole('button', { name: 'Remove' }).click();
      await dialog.getByRole('button', { name: 'Remove' }).click();
      await toast('Rule removed.').waitFor();
      assert.deepEqual((await api('GET', '/v1/admin/access', { user: ADMIN })).data.rules, []);
    });

    await step('the page works on a phone, and in the dark', async () => {
      await page.setViewportSize({ width: 390, height: 800 });
      await page.locator('nav a', { hasText: 'Themes' }).click();
      await page.getByRole('link', { name: /Midnight Test/ }).click();
      await page.getByRole('heading', { name: 'Midnight Test' }).waitFor();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert.ok(overflow <= 0, `the page scrolls sideways by ${overflow}px`);
      await shot('phone-editor');
      await page.emulateMedia({ colorScheme: 'dark' });
      await shot('phone-editor-dark');
      await page.setViewportSize({ width: 1280, height: 900 });
      await shot('desktop-editor-dark');
      await page.emulateMedia({ colorScheme: 'light' });
    });

    await step('signing out revokes the token on the server', async () => {
      const token = JSON.parse(await page.evaluate(() => sessionStorage.getItem('flare-admin-session'))).token;
      assert.equal((await api('GET', '/v1/account', { token })).status, 200);
      await page.getByRole('button', { name: 'Sign out' }).click();
      await page.getByRole('heading', { name: 'Flare admin' }).waitFor();
      assert.equal((await api('GET', '/v1/account', { token })).status, 401, 'the token must stop working');
      assert.equal(await page.evaluate(() => sessionStorage.getItem('flare-admin-session')), null);
    });

    await step('a token revoked elsewhere ends the session cleanly', async () => {
      await page.getByLabel('User name').fill(ADMIN.name);
      await page.getByLabel('Password').fill(ADMIN.password);
      await page.getByRole('button', { name: 'Sign in' }).click();
      await page.getByRole('heading', { name: 'Themes', exact: true }).waitFor();
      const session = JSON.parse(await page.evaluate(() => sessionStorage.getItem('flare-admin-session')));
      assert.equal((await api('DELETE', `/v1/account/tokens/${session.tokenId}`, { token: session.token })).status, 204);
      await page.locator('nav a', { hasText: 'Users' }).click();
      await toast('Your session ended').waitFor();
      await page.getByRole('heading', { name: 'Flare admin' }).waitFor();
    });

    await step('nothing was blocked by the content security policy, and nothing threw', async () => {
      const violations = await page.evaluate(() => window.__violations);
      assert.deepEqual(violations, []);
      assert.deepEqual(problems, []);
    });
    console.log('\nAdmin UI: all steps passed');
  } finally {
    await browser.close();
    server.child.kill();
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
