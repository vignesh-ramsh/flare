// Users: who can sign in, what they are, and the tokens that stay signed in.

import { h, field, toast, attempt, confirmDialog, dialog, formatTime } from '../ui.js';

export async function render(ctx) {
  const page = h('div', {});
  const me = ctx.api.session.user;

  async function draw() {
    const { users } = await ctx.api.users();
    page.replaceChildren(
      h('div', { class: 'page-head' },
        h('h1', {}, 'Users'),
        h('div', { class: 'tools' }, h('button', { type: 'button', class: 'primary', onclick: () => create() }, 'Add user'))),
      h('p', { class: 'lede' }, 'Administrators can use this page. A disabled user cannot sign in and their tokens stop working at once, but what they published and their access rules are kept.'),
      table(users));
  }

  async function reload() {
    try {
      await draw();
    } catch (error) {
      toast(error.message, 'error');
    }
  }

  function table(users) {
    if (users.length === 0) return h('div', { class: 'card empty' }, 'No users.');
    return h('div', { class: 'table-wrap' },
      h('table', {},
        h('thead', {}, h('tr', {}, h('th', {}, 'Name'), h('th', {}, 'Role'), h('th', {}, 'State'), h('th', {}, 'Added'), h('th', {}, h('span', { class: 'sr-only' }, 'Actions')))),
        h('tbody', {}, users.map(line))));
  }

  function line(u) {
    const self = u.name === me;
    const role = h('select', { 'aria-label': `Role of ${u.name}`, disabled: self },
      ['user', 'admin'].map((r) => h('option', { value: r, selected: r === u.role }, r)));
    role.addEventListener('change', async () => {
      if (await attempt(() => ctx.api.changeUser(u.name, { role: role.value }), `${u.name} is now ${role.value === 'admin' ? 'an administrator' : 'a user'}.`)) reload();
      else reload();
    });
    return h('tr', {},
      h('td', {}, h('strong', {}, u.name), self ? h('span', { class: 'badge' }, 'you') : null),
      h('td', {}, role),
      h('td', {}, u.disabled ? h('span', { class: 'badge err' }, 'Disabled') : h('span', { class: 'badge ok' }, 'Active')),
      h('td', {}, formatTime(u.created)),
      h('td', {}, h('div', { class: 'row' },
        h('button', { type: 'button', class: 'small', onclick: () => tokens(u) }, 'Tokens'),
        h('button', { type: 'button', class: 'small', onclick: () => password(u) }, 'Set password'),
        self ? null : h('button', { type: 'button', class: 'small', onclick: async () => {
          if (u.disabled || await confirmDialog(`Disable ${u.name}?`, 'They are signed out everywhere and cannot sign in until you enable them again.', 'Disable', true)) {
            if (await attempt(() => ctx.api.changeUser(u.name, { disabled: !u.disabled }), u.disabled ? `${u.name} is enabled.` : `${u.name} is disabled.`)) reload();
          }
        } }, u.disabled ? 'Enable' : 'Disable'),
        self ? null : h('button', { type: 'button', class: 'small danger', onclick: async () => {
          if (!(await confirmDialog(`Delete ${u.name}?`, 'Their tokens and access rules are deleted too. This cannot be undone.', 'Delete', true))) return;
          if (await attempt(() => ctx.api.deleteUser(u.name), `${u.name} is deleted.`)) reload();
        } }, 'Delete'))));
  }

  async function create() {
    const name = h('input', { type: 'text', autocomplete: 'off', maxlength: 64, spellcheck: 'false' });
    const pass = h('input', { type: 'password', autocomplete: 'new-password' });
    const role = h('select', {}, h('option', { value: 'user' }, 'User'), h('option', { value: 'admin' }, 'Administrator'));
    const problem = h('p', { class: 'error-text', role: 'alert', hidden: true });
    const done = await dialog('Add a user', (close) =>
      h('div', {},
        field('Name', name, 'Lower-case letters, digits, dots, dashes and underscores, up to 32.'),
        field('Password', pass, 'At least 10 characters. Share it with the person safely.'),
        field('Role', role),
        problem,
        h('div', { class: 'actions' },
          h('button', { type: 'button', onclick: () => close(undefined) }, 'Cancel'),
          h('button', { type: 'button', class: 'primary', onclick: async () => {
            try {
              await ctx.api.createUser({ name: name.value.trim(), password: pass.value, role: role.value });
              close(name.value.trim());
            } catch (e) {
              problem.textContent = e.message;
              problem.hidden = false;
            }
          } }, 'Add user'))));
    if (done) {
      toast(`${done} is added.`);
      reload();
    }
  }

  async function password(u) {
    const pass = h('input', { type: 'password', autocomplete: 'new-password' });
    const problem = h('p', { class: 'error-text', role: 'alert', hidden: true });
    await dialog(`New password for ${u.name}`, (close) =>
      h('div', {},
        field('Password', pass, 'At least 10 characters. Their tokens keep working; revoke them if the old password may be known.'),
        problem,
        h('div', { class: 'actions' },
          h('button', { type: 'button', onclick: () => close(false) }, 'Cancel'),
          h('button', { type: 'button', class: 'primary', onclick: async () => {
            try {
              await ctx.api.changeUser(u.name, { password: pass.value });
              toast(`Password of ${u.name} changed.`);
              close(true);
            } catch (e) {
              problem.textContent = e.message;
              problem.hidden = false;
            }
          } }, 'Change password'))));
  }

  async function tokens(u) {
    const body = h('div', {});
    async function load() {
      let list;
      try {
        ({ tokens: list } = await ctx.api.userTokens(u.name));
      } catch (e) {
        body.replaceChildren(h('p', { class: 'error-text' }, e.message));
        return;
      }
      body.replaceChildren(list.length === 0
        ? h('p', { class: 'help' }, 'No tokens: they are not signed in anywhere.')
        : h('div', { class: 'table-wrap' }, h('table', {},
          h('thead', {}, h('tr', {}, h('th', {}, 'Label'), h('th', {}, 'Last used'), h('th', {}, 'Expires'), h('th', {}, ''))),
          h('tbody', {}, list.map((t) => h('tr', {},
            h('td', {}, t.label || h('span', { class: 'help' }, '(no label)')),
            h('td', {}, formatTime(t.last_used)),
            h('td', {}, t.expires ? formatTime(t.expires) : 'never'),
            h('td', {}, h('button', { type: 'button', class: 'small danger', onclick: async () => {
              if (await attempt(() => ctx.api.revokeToken(u.name, t.id), 'Token revoked.')) load();
            } }, 'Revoke'))))))));
    }
    await load();
    await dialog(`Tokens of ${u.name}`, (close) =>
      h('div', {}, body, h('div', { class: 'actions' }, h('button', { type: 'button', onclick: () => close() }, 'Close'))));
  }

  await draw();
  return page;
}
