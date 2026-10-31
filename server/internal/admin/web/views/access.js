// Access: who may read or write which topics, beyond what everyone may do by default.

import { h, field, toast, attempt, confirmDialog, dialog } from '../ui.js';

const LEVELS = ['read-write', 'read-only', 'write-only', 'deny-all'];

export async function render(ctx) {
  const page = h('div', {});
  const { users } = await ctx.api.users();

  async function draw() {
    const { rules, default_access: fallback } = await ctx.api.access();
    page.replaceChildren(
      h('div', { class: 'page-head' },
        h('h1', {}, 'Access'),
        h('div', { class: 'tools' }, h('button', { type: 'button', class: 'primary', onclick: () => edit(null) }, 'Add rule'))),
      h('p', { class: 'lede' },
        `A topic nobody has a rule for is `, h('strong', {}, fallback),
        '. Rules name a user, or everyone, and a topic name or a pattern with * in it. The most specific rule wins, and an explicit deny beats an allow. Change the default under Settings.'),
      rules.length === 0 ? h('div', { class: 'card empty' }, 'No rules: everyone gets the default.') : table(rules));
  }

  async function reload() {
    try {
      await draw();
    } catch (error) {
      toast(error.message, 'error');
    }
  }

  function table(rules) {
    return h('div', { class: 'table-wrap' }, h('table', {},
      h('thead', {}, h('tr', {}, h('th', {}, 'Who'), h('th', {}, 'Topics'), h('th', {}, 'Access'), h('th', {}, ''))),
      h('tbody', {}, rules.map((r) => h('tr', {},
        h('td', {}, r.who === 'everyone' ? h('em', {}, 'everyone') : r.who),
        h('td', {}, h('code', {}, r.pattern)),
        h('td', {}, h('span', { class: `badge ${r.access === 'deny-all' ? 'err' : r.access === 'read-write' ? 'ok' : ''}` }, r.access)),
        h('td', {}, h('div', { class: 'row' },
          h('button', { type: 'button', class: 'small', onclick: () => edit(r) }, 'Change'),
          h('button', { type: 'button', class: 'small danger', onclick: async () => {
            if (!(await confirmDialog('Remove this rule?', `${r.who} → ${r.pattern} (${r.access})`, 'Remove', true))) return;
            if (await attempt(() => ctx.api.revoke(r.who, r.pattern), 'Rule removed.')) reload();
          } }, 'Remove'))))))));
  }

  async function edit(rule) {
    const who = h('select', { disabled: rule !== null },
      h('option', { value: 'everyone' }, 'Everyone'),
      users.map((u) => h('option', { value: u.name, selected: rule?.who === u.name }, u.name)));
    if (rule) who.value = rule.who;
    const pattern = h('input', { type: 'text', value: rule?.pattern ?? '', disabled: rule !== null, autocomplete: 'off', spellcheck: 'false', placeholder: 'alerts or build-*' });
    const level = h('select', {}, LEVELS.map((l) => h('option', { value: l, selected: l === (rule?.access ?? 'read-write') }, l)));
    const problem = h('p', { class: 'error-text', role: 'alert', hidden: true });
    const done = await dialog(rule ? 'Change a rule' : 'Add a rule', (close) =>
      h('div', {},
        field('Who', who),
        field('Topics', pattern, 'A topic name, or a pattern where * stands for any run of characters: build-* covers every topic that starts with build-.'),
        field('Access', level, 'read-write: read and publish. read-only: read. write-only: publish. deny-all: neither.'),
        problem,
        h('div', { class: 'actions' },
          h('button', { type: 'button', onclick: () => close(false) }, 'Cancel'),
          h('button', { type: 'button', class: 'primary', onclick: async () => {
            try {
              await ctx.api.grant({ who: who.value, pattern: pattern.value.trim(), access: level.value });
              close(true);
            } catch (e) {
              problem.textContent = e.message;
              problem.hidden = false;
            }
          } }, 'Save rule'))));
    if (done) {
      toast('Rule saved.');
      reload();
    }
  }

  await draw();
  return page;
}
