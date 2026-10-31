// Settings: the server's runtime settings. Each is saved on its own, so a mistake in one never
// holds up the others, and each can be put back to what the configuration file says.

import { h, field, toast, attempt, confirmDialog, formatSeconds } from '../ui.js';
import { isHex } from '../palette.js';

const UNITS = [['days', 86400], ['hours', 3600], ['minutes', 60], ['seconds', 1]];

export async function render(ctx) {
  const data = await ctx.api.settings();
  const page = h('div', {});
  const dirty = new Set();
  ctx.guard.dirty = () => dirty.size > 0;

  function draw(items, warnings) {
    page.replaceChildren();
    const pending = items.filter((i) => i.pending_restart);
    page.append(
      h('div', { class: 'page-head' }, h('h1', {}, 'Settings')),
      h('p', { class: 'lede' }, 'These override the configuration file and take effect straight away, unless marked “restart needed”. Reset one to go back to what the file says. Where the server listens, its database and the push credentials stay in the file.'));
    if (pending.length) {
      page.append(h('div', { class: 'notice warn', role: 'status' },
        `Waiting for a restart: ${pending.map((i) => i.label).join(', ')}. They apply the next time the server starts.`));
    }
    for (const w of warnings) page.append(h('div', { class: 'notice warn' }, w));
    for (const group of data.groups) {
      const card = h('section', { class: 'card' }, h('h2', {}, group.label));
      for (const item of items.filter((i) => i.group === group.id)) card.append(row(item));
      page.append(card);
    }
  }

  function replace(updated) {
    const i = data.settings.findIndex((s) => s.key === updated.key);
    data.settings[i] = updated;
    dirty.delete(updated.key);
    draw(data.settings, data.warnings);
  }

  function row(item) {
    const control = makeControl(item);
    const save = h('button', { type: 'button', class: 'small primary', disabled: true }, 'Save');
    const reset = h('button', { type: 'button', class: 'small', hidden: !item.overridden }, 'Reset');
    const problem = h('p', { class: 'help error-text', hidden: true });
    const current = JSON.stringify(item.value);
    control.onchange = () => {
      const value = control.read();
      const changed = value !== undefined && JSON.stringify(value) !== current;
      problem.hidden = value !== undefined;
      problem.textContent = value === undefined ? control.problem : '';
      save.disabled = !changed;
      if (changed) dirty.add(item.key);
      else dirty.delete(item.key);
    };
    save.addEventListener('click', async () => {
      const value = control.read();
      if (value === undefined) return;
      const updated = await attempt(() => ctx.api.setSetting(item.key, value));
      if (!updated) return;
      toast(updated.pending_restart ? `${item.label} saved. It applies after the server restarts.` : `${item.label} saved.`);
      replace(updated);
      if (item.key.startsWith('branding.')) ctx.reloadShared().catch(() => {});
    });
    reset.addEventListener('click', async () => {
      if (!(await confirmDialog(`Reset “${item.label}”?`, 'It goes back to what the configuration file, environment or flags say.', 'Reset'))) return;
      const updated = await attempt(() => ctx.api.resetSetting(item.key));
      if (!updated) return;
      toast(`${item.label} reset.`);
      replace(updated);
      if (item.key.startsWith('branding.')) ctx.reloadShared().catch(() => {});
    });
    return h('div', { class: 'setting' },
      h('div', {},
        h('div', {}, h('span', { class: 'label' }, item.label), h('span', { class: 'badges' },
          item.overridden ? h('span', { class: 'badge' }, 'Changed') : null,
          item.restart ? h('span', { class: 'badge warn' }, 'Restart needed') : null)),
        h('p', { class: 'help' }, item.help)),
      h('div', { class: 'value' },
        control.element,
        problem,
        h('div', { class: 'row' }, save, reset),
        item.overridden ? h('div', { class: 'default' }, `The configuration says: ${show(item, item.default)}`) : null));
  }

  draw(data.settings, data.warnings);
  return page;
}

function show(item, value) {
  if (item.type === 'seconds') return formatSeconds(value);
  if (item.type === 'bool') return value ? 'on' : 'off';
  if (item.type === 'list') return value.length ? value.join(', ') : 'none';
  return value === '' ? '(empty)' : String(value);
}

/**
 * The control for one setting: `element` to show, `read()` for the value it holds (undefined when
 * that is not valid, with `problem` saying why), and `onchange` to be set by the caller.
 */
function makeControl(item) {
  const c = { onchange: () => {}, problem: '' };
  const fire = () => c.onchange();
  const label = `${item.label}`;
  switch (item.type) {
    case 'bool': {
      const box = h('input', { type: 'checkbox', checked: Boolean(item.value), 'aria-label': label });
      const state = h('span', {}, item.value ? 'On' : 'Off');
      box.addEventListener('change', () => {
        state.textContent = box.checked ? 'On' : 'Off';
        fire();
      });
      c.element = h('div', { class: 'row' }, box, state);
      c.read = () => box.checked;
      break;
    }
    case 'enum': {
      const select = h('select', { 'aria-label': label }, item.enum.map((e) => h('option', { value: e, selected: e === item.value }, e)));
      select.addEventListener('change', fire);
      c.element = select;
      c.read = () => select.value;
      break;
    }
    case 'int': {
      const input = h('input', { type: 'number', min: item.min, max: item.max, step: 1, value: item.value, 'aria-label': label });
      input.addEventListener('input', fire);
      c.element = input;
      c.read = () => {
        const n = Number(input.value);
        if (input.value === '' || !Number.isInteger(n) || n < item.min || n > item.max) {
          c.problem = `Enter a whole number from ${item.min} to ${item.max}.`;
          return undefined;
        }
        return n;
      };
      break;
    }
    case 'seconds': {
      const unit = UNITS.find(([, size]) => item.value >= size && item.value % size === 0) ?? UNITS[3];
      const number = h('input', { type: 'number', min: 1, step: 1, value: item.value / unit[1], 'aria-label': label });
      const units = h('select', { 'aria-label': `${label}: unit` }, UNITS.map(([name, size]) => h('option', { value: size, selected: size === unit[1] }, name)));
      number.addEventListener('input', fire);
      units.addEventListener('change', fire);
      c.element = h('div', { class: 'row' }, number, units);
      c.read = () => {
        const n = Number(number.value) * Number(units.value);
        if (number.value === '' || !Number.isInteger(n) || n < item.min || n > item.max) {
          c.problem = `Enter a length of time from ${formatSeconds(item.min)} to ${formatSeconds(item.max)}.`;
          return undefined;
        }
        return n;
      };
      break;
    }
    case 'list': {
      const area = h('textarea', { rows: 4, 'aria-label': `${label}, one per line`, placeholder: 'One per line' });
      area.value = item.value.join('\n');
      area.addEventListener('input', fire);
      c.element = area;
      c.read = () => {
        const lines = area.value.split('\n').map((l) => l.trim()).filter(Boolean);
        if (item.max && lines.length > item.max) {
          c.problem = `At most ${item.max} entries.`;
          return undefined;
        }
        return lines;
      };
      break;
    }
    default: {
      const isColor = item.key === 'branding.primary_color';
      const input = h('input', { type: 'text', value: item.value, 'aria-label': label, maxlength: item.max ?? 200, autocomplete: 'off', spellcheck: 'false' });
      input.addEventListener('input', () => {
        if (picker && isHex(input.value)) picker.value = input.value.toLowerCase();
        fire();
      });
      const picker = isColor ? h('input', { type: 'color', value: isHex(item.value) ? item.value.toLowerCase() : '#000000', 'aria-label': `${label}: pick a colour` }) : null;
      picker?.addEventListener('input', () => {
        input.value = picker.value.toUpperCase();
        fire();
      });
      c.element = picker ? h('div', { class: 'row' }, picker, input) : input;
      c.read = () => {
        const text = input.value.trim();
        if (isColor && !isHex(text)) {
          c.problem = 'Enter a colour like #F97316.';
          return undefined;
        }
        const length = [...text].length;
        if (length < (item.min ?? 0) || length > (item.max ?? 200)) {
          c.problem = `Enter ${item.min ?? 0} to ${item.max ?? 200} characters.`;
          return undefined;
        }
        return text;
      };
    }
  }
  return c;
}
