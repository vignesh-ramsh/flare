// The Admin UI: signs an administrator in and shows one of four pages. It keeps no state of its
// own beyond the token in sessionStorage: what is shown is read from the server each time.

import { Api, loadSession } from './api.js';
import { isHex, onColor } from './palette.js';
import { h, clear, toast, field, attempt, confirmDialog } from './ui.js';
import * as themes from './views/themes.js';
import * as settings from './views/settings.js';
import * as users from './views/users.js';
import * as access from './views/access.js';

const PAGES = [
  { id: 'themes', label: 'Themes', view: themes },
  { id: 'settings', label: 'Settings', view: settings },
  { id: 'users', label: 'Users', view: users },
  { id: 'access', label: 'Access', view: access },
];

const root = document.getElementById('app');
const api = new Api(loadSession(), () => {
  toast('Your session ended. Sign in again.', 'error');
  start();
});

let config = null;
let contract = null;
// A page sets this while it holds changes that are not saved; navigating away then asks first.
const guard = { dirty: () => false };
let shownHash = '';

function parseHash() {
  const parts = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
  const page = PAGES.find((p) => p.id === parts[0]) ?? PAGES[0];
  return { page, args: parts.slice(1) };
}

function navigate(...parts) {
  location.hash = '#/' + parts.map(encodeURIComponent).join('/');
}

async function loadShared() {
  [config, contract] = await Promise.all([api.config(), api.contract()]);
  document.title = `${config.name} admin`;
  if (isHex(config.primary_color)) {
    const style = document.documentElement.style;
    style.setProperty('--accent', config.primary_color);
    style.setProperty('--on-accent', onColor(config.primary_color));
  }
}

async function start() {
  clear(root);
  try {
    if (!config) await loadShared();
  } catch (error) {
    root.append(h('main', {}, h('div', { class: 'notice err' }, error.message)));
    return;
  }
  if (!api.session) {
    showSignIn();
    return;
  }
  showShell();
}

function showSignIn() {
  document.body.classList.remove('signed-in');
  const message = h('p', { class: 'error-text', role: 'alert', hidden: true });
  const user = h('input', { type: 'text', name: 'user', autocomplete: 'username', required: true, autofocus: true });
  const password = h('input', { type: 'password', name: 'password', autocomplete: 'current-password', required: true });
  const submit = h('button', { type: 'submit', class: 'primary' }, 'Sign in');
  const form = h('form', { novalidate: true }, field('User name', user), field('Password', password), message, h('div', { class: 'actions' }, submit));
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    message.hidden = true;
    submit.disabled = true;
    try {
      await api.login(user.value.trim(), password.value);
      password.value = '';
      start();
    } catch (error) {
      message.textContent = error.status === 401 ? 'The user name or password is wrong.' : error.message;
      message.hidden = false;
      password.select();
    } finally {
      submit.disabled = false;
    }
  });
  clear(root).append(
    h('div', { class: 'signin' },
      h('div', { class: 'card' },
        h('h1', {}, h('span', { class: 'mark' }), `${config.name} admin`),
        h('p', { class: 'lede' }, 'Sign in with an administrator account.'),
        form)));
}

function showShell() {
  const nav = h('nav', { 'aria-label': 'Sections' }, PAGES.map((p) => h('a', { href: `#/${p.id}`, 'data-page': p.id }, p.label)));
  const signOut = h('button', { type: 'button', class: 'small', onclick: async () => {
    if (guard.dirty() && !(await confirmDialog('Discard changes?', 'You have changes that are not saved.', 'Discard', true))) return;
    guard.dirty = () => false;
    await api.logout();
    location.hash = '';
    start();
  } }, 'Sign out');
  const main = h('main', { id: 'page', tabindex: '-1' });
  clear(root).append(
    h('header', { class: 'topbar' },
      h('span', { class: 'brand' }, config.name),
      nav,
      h('span', { class: 'who' }, api.session.user, signOut)),
    main);
  shownHash = location.hash;
  show();
}

async function show() {
  const { page, args } = parseHash();
  for (const a of root.querySelectorAll('nav a')) {
    if (a.dataset.page === page.id) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  }
  const main = document.getElementById('page');
  if (!main) return;
  guard.dirty = () => false;
  clear(main).append(h('p', { class: 'lede' }, 'Loading…'));
  const ctx = { api, config, contract, guard, navigate, reloadShared: loadShared };
  try {
    const view = await page.view.render(ctx, args);
    clear(main).append(view);
  } catch (error) {
    clear(main).append(h('div', { class: 'notice err' }, error.message || 'Something went wrong.'));
  }
}

window.addEventListener('hashchange', async () => {
  if (!api.session || !document.getElementById('page')) return;
  const target = location.hash;
  if (target === shownHash) return;
  if (guard.dirty()) {
    const leave = await confirmDialog('Discard changes?', 'You have changes that are not saved.', 'Discard', true);
    if (!leave) {
      // Put the address back; replaceState does not raise another hashchange.
      history.replaceState(null, '', location.pathname + location.search + shownHash);
      return;
    }
  }
  shownHash = target;
  show();
});

// Closing the tab with changes that are not saved asks first.
window.addEventListener('beforeunload', (event) => {
  if (guard.dirty()) event.preventDefault();
});

start();
