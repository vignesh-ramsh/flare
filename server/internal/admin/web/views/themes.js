// Themes: the list, and an editor with a live preview and a check that text stays readable.

import { h, field, toast, attempt, confirmDialog, dialog, formatTime } from '../ui.js';
import { isHex, resolve, readability, blockingProblems, generatePalette } from '../palette.js';

// A theme read from a file, waiting to be opened in the editor as a new theme.
let draft = null;

export async function render(ctx, args) {
  const { themes } = await ctx.api.themes();
  if (args[0] === 'new') {
    const start = draft;
    draft = null;
    return editor(ctx, themes, null, start);
  }
  if (args[0]) {
    const found = themes.find((t) => t.name.toLowerCase() === args[0].toLowerCase());
    if (!found) {
      return h('div', {},
        h('div', { class: 'notice err' }, `There is no theme called "${args[0]}".`),
        h('a', { href: '#/themes' }, 'Back to the themes'));
    }
    return editor(ctx, themes, found, null);
  }
  return list(ctx, themes);
}

function strip(contract, theme) {
  const colors = resolve(contract, theme.colors, theme.brightness);
  const wrap = h('div', { class: 'strip', 'aria-hidden': 'true' });
  for (const key of ['primary', 'primary_container', 'secondary', 'tertiary', 'background', 'surface', 'on_surface']) {
    const cell = h('span');
    cell.style.background = colors[key];
    wrap.append(cell);
  }
  return wrap;
}

function list(ctx, themes) {
  const grid = h('div', { class: 'theme-grid' },
    themes.map((t) =>
      h('a', { class: 'theme-card', href: `#/themes/${encodeURIComponent(t.name)}` },
        h('h3', {}, t.name),
        h('div', { class: 'meta' }, `${t.brightness === 'dark' ? 'Dark' : 'Light'} · ${Object.keys(t.colors).length} colours set · changed ${formatTime(t.updated)}`),
        strip(ctx.contract, t))));
  return h('div', {},
    h('div', { class: 'page-head' },
      h('h1', {}, 'Themes'),
      h('div', { class: 'tools' },
        h('button', { type: 'button', onclick: () => importTheme(ctx) }, 'Import…'),
        h('button', { type: 'button', class: 'primary', onclick: () => ctx.navigate('themes', 'new') }, 'New theme'))),
    h('p', { class: 'lede' },
      'Themes added here are downloaded by every app that connects to this server, and appear in Settings → Appearance next to the themes the app ships with. People choose one for themselves.'),
    themes.length === 0
      ? h('div', { class: 'card empty' }, h('p', {}, 'No themes yet.'), h('button', { type: 'button', class: 'primary', onclick: () => ctx.navigate('themes', 'new') }, 'Add the first theme'))
      : grid);
}

async function importTheme(ctx) {
  const text = h('textarea', { rows: 10, 'aria-label': 'Theme JSON', placeholder: '{"name": "Midnight", "brightness": "dark", "colors": {"primary": "#8AB4F8"}}' });
  const file = h('input', { type: 'file', accept: '.json,application/json', 'aria-label': 'Choose a theme file' });
  file.addEventListener('change', async () => {
    if (file.files[0]) text.value = await file.files[0].text();
  });
  const error = h('p', { class: 'error-text', role: 'alert', hidden: true });
  const result = await dialog('Import a theme', (close) =>
    h('div', {},
      h('p', { class: 'help' }, 'Paste a theme exported from here, or choose its file. You can look it over and change it before it is saved.'),
      file, text, error,
      h('div', { class: 'actions' },
        h('button', { type: 'button', onclick: () => close(undefined) }, 'Cancel'),
        h('button', { type: 'button', class: 'primary', onclick: () => {
          try {
            close(parseTheme(text.value, ctx.contract));
          } catch (e) {
            error.textContent = e.message;
            error.hidden = false;
          }
        } }, 'Open in the editor'))));
  if (result) {
    draft = result;
    ctx.navigate('themes', 'new');
  }
}

/** Reads a theme from JSON text, or throws what is wrong in words. */
export function parseTheme(text, contract) {
  let data;
  try {
    data = JSON.parse(text);
  } catch {
    throw new Error('That is not valid JSON.');
  }
  if (data === null || typeof data !== 'object' || Array.isArray(data)) throw new Error('A theme is a JSON object with a name, a brightness and colours.');
  const known = new Set(contract.tokens.map((t) => t.key));
  if (typeof data.name !== 'string') throw new Error('The theme has no name.');
  if (data.brightness !== 'light' && data.brightness !== 'dark') throw new Error('The brightness must be "light" or "dark".');
  if (data.colors === null || typeof data.colors !== 'object' || Array.isArray(data.colors)) throw new Error('The theme has no colours.');
  const colors = {};
  for (const [key, value] of Object.entries(data.colors)) {
    if (!known.has(key)) continue;
    if (!isHex(value)) throw new Error(`${key} must be a colour like #RRGGBB.`);
    colors[key] = value.trim().toUpperCase();
  }
  if (!colors.primary) throw new Error('The theme needs a primary colour.');
  return { name: data.name, brightness: data.brightness, colors };
}

function download(filename, text) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
  const link = h('a', { href: url, download: filename });
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

const cssName = (key) => `--t-${key.replaceAll('_', '-')}`;

function previewBox() {
  const box = h('div', { class: 'preview', role: 'img', 'aria-label': 'A preview of the app in this theme' },
    h('div', { class: 'bar' }, 'Flare'),
    h('div', { class: 'body' },
      h('div', { class: 'tile' }, h('div', {}, 'deploys'), h('div', { class: 'sub' }, 'Build 482 finished')),
      h('div', { class: 'tile' }, h('div', {}, 'backups'), h('div', { class: 'sub' }, 'Nightly backup done')),
      h('div', { class: 'bubble' }, 'Disk usage is at 82%'),
      h('div', { class: 'bubble urgent' }, 'Urgent: service is down'),
      h('div', { class: 'chips' }, h('span', { class: 'chip secondary' }, 'Secondary'), h('span', { class: 'chip accent' }, 'High')),
      h('div', { class: 'field-sample' }, 'Message…'),
      h('div', { class: 'btns' },
        h('span', { class: 'sample-button btn-primary' }, 'Primary'),
        h('span', { class: 'sample-button btn-secondary' }, 'Secondary'),
        h('span', { class: 'sample-button btn-error' }, 'Delete')),
      h('div', { class: 'snack' }, 'Topic copied')));
  return box;
}

function editor(ctx, existing, theme, imported) {
  const { api, contract } = ctx;
  const isNew = theme === null;
  const initial = theme ?? imported ?? { name: '', brightness: 'light', colors: { primary: '#2F5D8C' } };
  const state = { name: initial.name, brightness: initial.brightness, colors: { ...initial.colors } };
  // Opened from a file, the theme is not yet saved, so leaving it asks first.
  let saved = isNew && imported ? '' : JSON.stringify(state);
  const dirty = () => JSON.stringify(state) !== saved;
  ctx.guard.dirty = dirty;

  const labelOf = Object.fromEntries(contract.tokens.map((t) => [t.key, t.label]));
  const namePattern = new RegExp(contract.name_pattern);

  // ---- name and brightness
  const nameInput = h('input', { type: 'text', value: state.name, maxlength: 32, autocomplete: 'off', spellcheck: 'false', disabled: !isNew });
  const nameProblem = h('p', { class: 'help error-text', hidden: true });
  const brightness = h('select', { 'aria-label': 'Brightness' },
    h('option', { value: 'light', selected: state.brightness === 'light' }, 'Light'),
    h('option', { value: 'dark', selected: state.brightness === 'dark' }, 'Dark'));

  // ---- colours
  const rows = {};
  const colorGroups = contract.groups.map((g) => {
    const box = h('section', { class: 'token-group' }, h('h3', {}, g.label));
    for (const token of contract.tokens.filter((t) => t.group === g.id)) {
      const required = contract.required_tokens.includes(token.key);
      const picker = h('input', { type: 'color', 'aria-label': `${token.label}: pick a colour` });
      const text = h('input', { type: 'text', maxlength: 7, spellcheck: 'false', autocomplete: 'off', 'aria-label': `${token.label}: colour code` });
      const clearBtn = h('button', { type: 'button', class: 'small quiet', title: 'Leave this colour to be worked out', 'aria-label': `${token.label}: leave to be worked out`, onclick: () => {
        delete state.colors[token.key];
        refresh();
      } }, '×');
      const derived = h('div', { class: 'derived' });
      picker.addEventListener('input', () => {
        state.colors[token.key] = picker.value.toUpperCase();
        refresh(token.key);
      });
      text.addEventListener('input', () => {
        const v = text.value.trim();
        if (isHex(v)) {
          state.colors[token.key] = v.toUpperCase();
          text.setAttribute('aria-invalid', 'false');
          refresh(token.key);
        } else if (v === '' && !required) {
          delete state.colors[token.key];
          text.setAttribute('aria-invalid', 'false');
          refresh(token.key);
        } else {
          text.setAttribute('aria-invalid', 'true');
        }
      });
      text.addEventListener('blur', () => {
        text.setAttribute('aria-invalid', 'false');
        refresh();
      });
      rows[token.key] = { picker, text, clearBtn, derived, required };
      box.append(h('div', { class: 'token' },
        h('div', {}, h('div', { class: 'name' }, token.label), h('div', { class: 'help' }, token.help), derived),
        h('div', { class: 'controls' }, picker, text, required ? h('span', { class: 'clear-slot' }) : clearBtn)));
    }
    return box;
  });

  // ---- preview and readability
  const preview = previewBox();
  const ratios = h('ul', { class: 'ratio-list' });
  const summary = h('p', { class: 'help' });
  const saveBtn = h('button', { type: 'button', class: 'primary' }, isNew ? 'Add theme' : 'Save changes');
  const saveNote = h('p', { class: 'help error-text', role: 'alert', hidden: true });

  function pairLabel(p) {
    const t = labelOf[p.text];
    return t.startsWith('Text on ') ? t : `${t} on ${labelOf[p.background].toLowerCase()}`;
  }

  function refresh(editing) {
    const colors = resolve(contract, state.colors, state.brightness);
    for (const [key, row] of Object.entries(rows)) {
      const isSet = isHex(state.colors[key]);
      if (key !== editing) {
        row.picker.value = colors[key].toLowerCase();
        if (document.activeElement !== row.text) row.text.value = isSet ? state.colors[key] : '';
      }
      row.text.placeholder = colors[key];
      row.derived.textContent = isSet ? '' : 'worked out from the other colours';
      row.derived.hidden = isSet;
      row.clearBtn.hidden = !isSet;
    }
    for (const [key, value] of Object.entries(colors)) preview.style.setProperty(cssName(key), value);

    ratios.replaceChildren();
    const results = readability(contract, state.colors, state.brightness);
    for (const r of results) {
      const sample = h('span', { class: 'sample' }, 'Aa');
      sample.style.color = colors[r.text];
      sample.style.background = colors[r.background];
      ratios.append(h('li', {},
        sample,
        h('span', { class: 'label' }, pairLabel(r)),
        h('span', { class: 'mono' }, `${r.ratio.toFixed(1)}:1`),
        h('span', { class: `badge ${r.level === 'ok' ? 'ok' : r.level === 'warn' ? 'warn' : 'err'}` }, r.level === 'ok' ? 'Good' : r.level === 'warn' ? 'Low' : 'Too low')));
    }
    const failing = blockingProblems(contract, state.colors, state.brightness);
    const weak = results.filter((r) => r.level !== 'ok').length;
    summary.textContent = weak === 0
      ? 'All text is easy to read.'
      : `${weak} pair${weak === 1 ? '' : 's'} below ${contract.recommended_contrast}:1, the level WCAG advises for ordinary text.`;
    const problem = nameProblemText();
    nameProblem.textContent = problem;
    nameProblem.hidden = problem === '';
    const blocked = failing.length > 0 || problem !== '' || state.name === '';
    saveBtn.disabled = blocked || (!isNew && !dirty());
    saveNote.hidden = failing.length === 0;
    saveNote.textContent = failing.length ? `The server will not accept this: ${failing.map(pairLabel).join(', ')} ${failing.length === 1 ? 'is' : 'are'} below ${contract.minimum_contrast}:1.` : '';
  }

  function nameProblemText() {
    if (!isNew) return '';
    if (state.name === '') return '';
    if (!namePattern.test(state.name)) return 'Use 1 to 32 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit.';
    if (existing.some((t) => t.name.toLowerCase() === state.name.toLowerCase())) return 'A theme with this name exists. Names are not case-sensitive.';
    return '';
  }

  nameInput.addEventListener('input', () => {
    state.name = nameInput.value.trim();
    refresh();
  });
  brightness.addEventListener('change', () => {
    state.brightness = brightness.value;
    refresh();
  });

  // ---- actions
  saveBtn.addEventListener('click', async () => {
    const body = { name: state.name, brightness: state.brightness, colors: state.colors };
    const done = await attempt(() => (isNew ? api.createTheme(body) : api.replaceTheme(body)), 'Saved. Apps pick the theme up the next time they connect.');
    if (!done) return;
    saved = JSON.stringify(state);
    if (isNew) ctx.navigate('themes', state.name);
    else refresh();
  });

  const fillBtn = h('button', { type: 'button', onclick: () => {
    const colors = resolve(contract, state.colors, state.brightness);
    state.colors = { ...colors };
    refresh();
    toast('Every colour is now set, so the app has nothing left to work out.');
  } }, 'Set every colour');
  const regenBtn = h('button', { type: 'button', onclick: async () => {
    const others = Object.keys(state.colors).filter((k) => k !== 'primary').length;
    if (others > 0 && !(await confirmDialog('Start again from the primary colour?', 'Every colour but the primary is replaced by one chosen to go with it.', 'Replace', true))) return;
    state.colors = generatePalette(contract, { brightness: state.brightness, primary: state.colors.primary });
    refresh();
  } }, 'Rebuild from the primary');

  const tools = h('div', { class: 'tools' });
  if (!isNew) {
    tools.append(
      h('button', { type: 'button', onclick: () => download(`${theme.name}.theme.json`, JSON.stringify({ name: state.name, brightness: state.brightness, colors: state.colors }, null, 2) + '\n') }, 'Export'),
      h('button', { type: 'button', onclick: () => duplicate() }, 'Duplicate'),
      h('button', { type: 'button', class: 'danger', onclick: async () => {
        if (!(await confirmDialog(`Delete "${theme.name}"?`, 'It disappears from the apps the next time they connect. People who chose it go back to the default theme.', 'Delete', true))) return;
        if (await attempt(() => api.deleteTheme(theme.name), 'Deleted.')) {
          saved = JSON.stringify(state);
          ctx.navigate('themes');
        }
      } }, 'Delete'));
  }

  async function duplicate() {
    const input = h('input', { type: 'text', maxlength: 32, value: `${theme.name} copy`.slice(0, 32), autocomplete: 'off' });
    const name = await dialog('Duplicate theme', (close) =>
      h('div', {}, field('Name of the copy', input),
        h('div', { class: 'actions' },
          h('button', { type: 'button', onclick: () => close(undefined) }, 'Cancel'),
          h('button', { type: 'button', class: 'primary', onclick: () => close(input.value.trim()) }, 'Duplicate'))));
    if (!name) return;
    if (await attempt(() => api.createTheme({ name, brightness: state.brightness, colors: state.colors }), 'Duplicated.')) {
      saved = JSON.stringify(state);
      ctx.navigate('themes', name);
    }
  }

  refresh();
  return h('div', {},
    h('div', { class: 'page-head' },
      h('div', {}, h('p', {}, h('a', { href: '#/themes' }, '← All themes')), h('h1', {}, isNew ? 'New theme' : theme.name)),
      tools),
    h('div', { class: 'editor' },
      h('div', {},
        h('div', { class: 'card' },
          field('Name', nameInput, isNew ? 'Unique on this server; letters, digits, spaces, dots, dashes and underscores.' : 'A theme keeps its name. To use another, duplicate it.'),
          nameProblem,
          field('Brightness', brightness, 'Whether the theme is for light or dark surroundings. The app uses it for the status bar and for colours you leave out.'),
          h('p', { class: 'help' }, 'Only the primary colour is required. Any colour left unset is worked out from the others, so a theme can be as small as one colour.'),
          h('div', { class: 'row' }, fillBtn, regenBtn)),
        colorGroups),
      h('div', { class: 'side' },
        h('div', { class: 'card' }, h('h2', {}, 'Preview'), preview,
          h('p', { class: 'help' }, 'An approximation. The app works out unset colours with Material 3, so they may differ a little; set a colour to be sure of it.')),
        h('div', { class: 'card' }, h('h2', {}, 'Can it be read?'), summary, ratios))),
    // Always in reach: the colours make the page long.
    h('div', { class: 'savebar' }, saveNote, saveBtn));
}
