// Colour maths for themes: parsing, blending, contrast, and filling in a whole palette from a few
// colours. Pure functions with no page in them, so they can be tested on their own.

const HEX = /^#[0-9a-fA-F]{6}$/;

export function isHex(text) {
  return typeof text === 'string' && HEX.test(text.trim());
}

function rgb(hex) {
  const v = parseInt(hex.trim().slice(1), 16);
  return [(v >> 16) & 255, (v >> 8) & 255, v & 255];
}

export function toHex(channels) {
  return '#' + channels.map((c) => Math.max(0, Math.min(255, Math.round(c))).toString(16).padStart(2, '0')).join('').toUpperCase();
}

/** `over` laid on `under` at opacity `alpha`. */
export function blend(over, under, alpha) {
  const o = rgb(over);
  const u = rgb(under);
  return toHex(o.map((c, i) => c * alpha + u[i] * (1 - alpha)));
}

export function luminance(hex) {
  const channel = (c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  const [r, g, b] = rgb(hex);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/** The WCAG contrast ratio of two colours, 1 to 21. */
export function contrast(a, b) {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/** Black or white, whichever reads better on `background`. */
export function onColor(background) {
  return contrast(background, '#000000') >= contrast(background, '#FFFFFF') ? '#000000' : '#FFFFFF';
}

/** `text` if it reads on `background`, otherwise black or white. */
function soft(text, background) {
  return contrast(text, background) >= 4.5 ? text : onColor(background);
}

const DEFAULTS = {
  light: { surface: '#FFFFFF', background: '#F4F5F7', text: '#1B1F24', secondary: '#5F6B76', tertiary: '#8A5A00', error: '#B3261E' },
  dark: { surface: '#1A1C20', background: '#111317', text: '#E6E8EB', secondary: '#A8B0B9', tertiary: '#F2C879', error: '#F2B8B5' },
};

/**
 * Every colour of a theme, from a primary colour and, optionally, the page, surface and text
 * colours and a few others. What is not given comes from sensible defaults for the brightness.
 * Text on each colour is chosen to be readable on it.
 *
 * The result has the colours in the order of `contract.tokens`.
 */
export function generatePalette(contract, opts) {
  const dark = opts.brightness === 'dark';
  const d = DEFAULTS[dark ? 'dark' : 'light'];
  const primary = opts.primary;
  const surface = opts.surface ?? d.surface;
  const background = opts.background ?? d.background;
  const text = opts.text ?? d.text;
  const secondary = opts.secondary ?? d.secondary;
  const tertiary = opts.tertiary ?? d.tertiary;
  const error = opts.error ?? d.error;

  const c = {};
  c.primary = primary;
  c.on_primary = onColor(primary);
  c.primary_container = blend(primary, surface, dark ? 0.28 : 0.18);
  c.on_primary_container = soft(text, c.primary_container);
  c.secondary = secondary;
  c.on_secondary = onColor(secondary);
  c.secondary_container = blend(secondary, surface, dark ? 0.28 : 0.18);
  c.on_secondary_container = soft(text, c.secondary_container);
  c.tertiary = tertiary;
  c.on_tertiary = onColor(tertiary);
  c.tertiary_container = blend(tertiary, surface, dark ? 0.32 : 0.22);
  c.on_tertiary_container = soft(text, c.tertiary_container);
  c.error = error;
  c.on_error = onColor(error);
  c.error_container = dark ? '#8C1D18' : '#F9DEDC';
  c.on_error_container = soft(text, c.error_container);
  c.background = background;
  c.surface = surface;
  c.surface_container_lowest = blend(dark ? '#000000' : '#FFFFFF', surface, 0.55);
  c.surface_container_low = blend(text, surface, 0.03);
  c.surface_container = blend(text, surface, 0.06);
  c.surface_container_high = blend(text, surface, 0.09);
  c.surface_container_highest = blend(text, surface, 0.12);
  c.on_surface = text;
  c.on_surface_variant = blend(text, surface, 0.78);
  c.border = opts.border ?? blend(text, surface, 0.55);
  c.border_subtle = blend(text, surface, 0.18);
  c.shadow = '#000000';
  c.scrim = '#000000';
  c.inverse_surface = text;
  c.on_inverse_surface = surface;
  c.inverse_primary = dark ? blend(primary, '#000000', 0.6) : blend(primary, '#FFFFFF', 0.5);

  return Object.fromEntries(contract.tokens.map((t) => [t.key, c[t.key]]));
}

/**
 * The colours a theme would have: those it sets, and for the rest an approximation built from
 * the ones it does set. (The app derives them with Material 3's palette, which is close but not
 * identical; the editor says so.)
 */
export function resolve(contract, colors, brightness) {
  const set = Object.fromEntries(Object.entries(colors).filter(([, v]) => isHex(v)));
  const primary = set.primary ?? (brightness === 'dark' ? '#8AB4F8' : '#2F5D8C');
  const base = generatePalette(contract, {
    brightness,
    primary,
    surface: set.surface,
    background: set.background,
    text: set.on_surface,
    secondary: set.secondary,
    tertiary: set.tertiary,
    error: set.error,
    border: set.border,
  });
  const out = {};
  for (const t of contract.tokens) out[t.key] = (set[t.key] ?? base[t.key]).toUpperCase();
  return out;
}

/**
 * How readable each pair of text and background is. `explicit` says the theme sets both, so the
 * server checks it; `level` is `fail` below the minimum, `warn` below what WCAG asks of ordinary
 * text, otherwise `ok`.
 */
export function readability(contract, colors, brightness) {
  const resolved = resolve(contract, colors, brightness);
  const set = (k) => isHex(colors[k]);
  return contract.readable_pairs.map((p) => {
    const ratio = contrast(resolved[p.text], resolved[p.background]);
    const level = ratio < contract.minimum_contrast ? 'fail' : ratio < contract.recommended_contrast ? 'warn' : 'ok';
    return { text: p.text, background: p.background, ratio, level, explicit: set(p.text) && set(p.background) };
  });
}

/** Whether the server would refuse the theme for unreadable text: a pair it sets that fails. */
export function blockingProblems(contract, colors, brightness) {
  return readability(contract, colors, brightness).filter((r) => r.explicit && r.level === 'fail');
}
