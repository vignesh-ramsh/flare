// Small helpers for building the page without a framework: elements, toasts and dialogs.
// Text is always set as text, never as HTML, so nothing a server returns can become markup.

/** `h('button', { class: 'primary', onclick: fn }, 'Save')` */
export function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [name, value] of Object.entries(props ?? {})) {
    if (value === false || value === null || value === undefined) continue;
    if (name === 'class') el.className = value;
    else if (name.startsWith('on') && typeof value === 'function') el.addEventListener(name.slice(2).toLowerCase(), value);
    else if (name === 'value') el.value = value;
    else if (name === 'checked' || name === 'disabled' || name === 'selected' || name === 'open') el[name] = value;
    else if (value === true) el.setAttribute(name, '');
    else el.setAttribute(name, String(value));
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
}

export function clear(el) {
  el.replaceChildren();
  return el;
}

const MAX_TOASTS = 3;

/** A short message that says what happened. It goes away by itself, or when clicked; errors stay longer. */
export function toast(message, kind = 'ok') {
  const region = document.getElementById('toasts');
  const item = h('div', { class: `toast ${kind}`, title: 'Click to dismiss', onclick: () => item.remove() }, message);
  region.append(item);
  // A burst of messages must not cover the page: keep the latest few.
  while (region.children.length > MAX_TOASTS) region.firstElementChild.remove();
  setTimeout(() => item.remove(), kind === 'error' ? 9000 : 4000);
}

/** Runs `action`, telling the user how it went. Returns what it returned, or undefined on failure. */
export async function attempt(action, success) {
  try {
    const result = await action();
    if (success) toast(success);
    return result ?? true;
  } catch (error) {
    toast(error.message || 'Something went wrong.', 'error');
    return undefined;
  }
}

/** A modal dialog. `build(close)` returns its contents; the promise settles with what is passed to `close`. */
export function dialog(title, build) {
  return new Promise((resolve) => {
    const d = h('dialog', { class: 'modal', 'aria-labelledby': 'dialog-title' });
    const close = (value) => {
      d.close();
      d.remove();
      resolve(value);
    };
    d.append(h('h2', { id: 'dialog-title' }, title), build(close));
    d.addEventListener('cancel', () => close(undefined));
    document.body.append(d);
    d.showModal();
    d.querySelector('input, select, textarea, button')?.focus();
  });
}

export function confirmDialog(title, text, confirmLabel, danger = false) {
  return dialog(title, (close) =>
    h('div', {},
      h('p', {}, text),
      h('div', { class: 'actions' },
        h('button', { type: 'button', onclick: () => close(false) }, 'Cancel'),
        h('button', { type: 'button', class: danger ? 'danger' : 'primary', onclick: () => close(true) }, confirmLabel))));
}

/** A labelled field: `field('Name', input, 'Help text')`. */
export function field(label, control, help) {
  const id = control.id || `f${Math.random().toString(36).slice(2, 9)}`;
  control.id = id;
  return h('div', { class: 'field' },
    h('label', { for: id }, label),
    control,
    help ? h('p', { class: 'help', id: `${id}-help` }, help) : null);
}

export function formatTime(unix) {
  if (!unix) return 'never';
  return new Date(unix * 1000).toLocaleString();
}

export function formatSeconds(total) {
  const units = [['day', 86400], ['hour', 3600], ['minute', 60], ['second', 1]];
  for (const [name, size] of units) {
    if (total >= size && total % size === 0) {
      const n = total / size;
      return `${n} ${name}${n === 1 ? '' : 's'}`;
    }
  }
  return `${total} seconds`;
}
