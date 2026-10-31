// Unit tests for the Admin UI's colour maths and theme reader. Run with `node --test e2e/`.
// They need no browser and no server: the code under test is plain functions.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { blend, blockingProblems, contrast, generatePalette, isHex, luminance, onColor, readability, resolve, toHex } from '../internal/admin/web/palette.js';
import { parseTheme } from '../internal/admin/web/views/themes.js';

const contract = JSON.parse(readFileSync(fileURLToPath(new URL('../../api/theme-tokens.json', import.meta.url)), 'utf8'));

test('hex colours are recognised strictly', () => {
  for (const ok of ['#000000', '#FFFFFF', '#8ab4f8', ' #8AB4F8 ']) assert.equal(isHex(ok), true, ok);
  for (const bad of ['', '#fff', '8AB4F8', '#GGGGGG', '#12345', '#1234567', 'red', null, undefined, 5]) assert.equal(isHex(bad), false, String(bad));
});

test('contrast follows WCAG', () => {
  assert.equal(contrast('#000000', '#FFFFFF').toFixed(2), '21.00');
  assert.equal(contrast('#123456', '#123456').toFixed(2), '1.00');
  // The classic borderline greys on white: #767676 passes 4.5, #777777 does not.
  assert.ok(contrast('#767676', '#FFFFFF') >= 4.5);
  assert.ok(contrast('#777777', '#FFFFFF') < 4.5);
  assert.equal(contrast('#336699', '#FFFFFF'), contrast('#FFFFFF', '#336699'), 'order does not matter');
  assert.equal(luminance('#000000'), 0);
  assert.ok(Math.abs(luminance('#FFFFFF') - 1) < 1e-9);
});

test('onColor picks black or white, whichever reads better', () => {
  assert.equal(onColor('#000000'), '#FFFFFF');
  assert.equal(onColor('#FFFFFF'), '#000000');
  assert.equal(onColor('#8AB4F8'), '#000000');
  assert.equal(onColor('#1A237E'), '#FFFFFF');
  for (let v = 0; v < 256; v += 5) {
    const bg = toHex([v, v, v]);
    const chosen = onColor(bg);
    const other = chosen === '#000000' ? '#FFFFFF' : '#000000';
    assert.ok(contrast(bg, chosen) >= contrast(bg, other), bg);
    assert.ok(contrast(bg, chosen) >= 4.5, `${bg} reads at ${contrast(bg, chosen)}`);
  }
});

test('blend mixes colours and clamps to a valid colour', () => {
  assert.equal(blend('#000000', '#FFFFFF', 0.5), '#808080');
  assert.equal(blend('#FF0000', '#0000FF', 1), '#FF0000');
  assert.equal(blend('#FF0000', '#0000FF', 0), '#0000FF');
  assert.equal(toHex([300, -5, 12.4]), '#FF000C');
});

function hsl(h, s, l) {
  const a = s * Math.min(l, 1 - l);
  const f = (n) => {
    const k = (n + h / 30) % 12;
    return l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1));
  };
  return toHex([f(0) * 255, f(8) * 255, f(4) * 255]);
}

test('a generated palette has every colour, in the contract order, and every text is readable', () => {
  const keys = contract.tokens.map((t) => t.key);
  let checked = 0;
  for (const brightness of ['light', 'dark']) {
    for (let hue = 0; hue < 360; hue += 15) {
      for (const s of [0.05, 0.4, 0.9]) {
        for (const l of [0.08, 0.2, 0.35, 0.5, 0.65, 0.8, 0.95]) {
          const primary = hsl(hue, s, l);
          const palette = generatePalette(contract, { brightness, primary });
          assert.deepEqual(Object.keys(palette), keys, 'the colours come in the contract order');
          for (const [k, v] of Object.entries(palette)) assert.ok(isHex(v), `${k} is ${v}`);
          assert.equal(palette.primary, primary);
          for (const pair of contract.readable_pairs) {
            const ratio = contrast(palette[pair.text], palette[pair.background]);
            assert.ok(ratio >= contract.recommended_contrast, `${brightness} ${primary}: ${pair.text} on ${pair.background} is ${ratio.toFixed(2)}`);
            checked++;
          }
        }
      }
    }
  }
  assert.ok(checked > 10000, `only ${checked} pairs were checked`);
});

test('resolve fills what is unset and keeps what is set', () => {
  const only = resolve(contract, { primary: '#8AB4F8' }, 'dark');
  assert.deepEqual(Object.keys(only), contract.tokens.map((t) => t.key));
  assert.equal(only.primary, '#8AB4F8');
  const set = resolve(contract, { primary: '#8AB4F8', surface: '#101010', on_surface: '#EEEEEE' }, 'dark');
  assert.equal(set.surface, '#101010');
  assert.equal(set.on_surface, '#EEEEEE');
  // Colours worked out from the surface follow the surface that was chosen.
  assert.notEqual(set.surface_container, only.surface_container);
  // Nothing is set: still something to show.
  assert.ok(isHex(resolve(contract, {}, 'light').primary));
  // Junk is ignored, not shown.
  assert.equal(resolve(contract, { primary: '#8AB4F8', surface: 'nonsense' }, 'dark').surface, only.surface);
});

test('only pairs the theme sets itself can stop it being saved', () => {
  // Left to be worked out, text is always readable, so nothing blocks.
  assert.deepEqual(blockingProblems(contract, { primary: '#8AB4F8' }, 'dark'), []);
  // Both set, and the same colour: the server would refuse.
  const same = blockingProblems(contract, { primary: '#8AB4F8', on_primary: '#8AB4F8' }, 'dark');
  assert.deepEqual(same.map((p) => `${p.text}/${p.background}`), ['on_primary/primary']);
  // Text set against a background that is worked out: it is not what the server checks.
  const worked = readability(contract, { primary: '#8AB4F8', on_surface: '#0A0A0A' }, 'dark').find((r) => r.text === 'on_surface' && r.background === 'surface');
  assert.equal(worked.explicit, false);
  assert.equal(worked.level, 'fail', 'it is still shown as unreadable');
  // The levels follow the contract's two numbers.
  const levels = readability(contract, { primary: '#808080', on_primary: '#FFFFFF' }, 'light').find((r) => r.text === 'on_primary');
  assert.equal(levels.level, contrast('#FFFFFF', '#808080') < 3 ? 'fail' : contrast('#FFFFFF', '#808080') < 4.5 ? 'warn' : 'ok');
});

test('parseTheme reads a theme and says what is wrong with one that is not', () => {
  const good = parseTheme(JSON.stringify({ name: 'Mine', brightness: 'dark', colors: { primary: '#8ab4f8', on_primary: '#000000', future_colour: '#123456' } }), contract);
  assert.deepEqual(good, { name: 'Mine', brightness: 'dark', colors: { primary: '#8AB4F8', on_primary: '#000000' } }, 'upper-cased, and colours this version does not know are left out');
  const refuse = (value, message) => assert.throws(() => parseTheme(typeof value === 'string' ? value : JSON.stringify(value), contract), message);
  refuse('{nope', /not valid JSON/);
  refuse('[]', /JSON object/);
  refuse('null', /JSON object/);
  refuse({ brightness: 'dark', colors: { primary: '#8AB4F8' } }, /no name/);
  refuse({ name: 'x', brightness: 'grey', colors: { primary: '#8AB4F8' } }, /light.*dark/);
  refuse({ name: 'x', brightness: 'dark' }, /no colours/);
  refuse({ name: 'x', brightness: 'dark', colors: { primary: 'blue' } }, /primary must be a colour/);
  refuse({ name: 'x', brightness: 'dark', colors: { on_primary: '#000000' } }, /needs a primary/);
});

test('text the theme chose is replaced where it would not read on a worked-out colour', () => {
  // A quiet grey is fine on white but not on the tinted container made from the primary.
  const palette = generatePalette(contract, { brightness: 'light', primary: '#1A237E', text: '#7A7A7A' });
  assert.equal(palette.on_surface, '#7A7A7A', 'the chosen text is kept where it is the text');
  for (const pair of contract.readable_pairs.filter((p) => p.background.endsWith('_container'))) {
    assert.ok(contrast(palette[pair.text], palette[pair.background]) >= contract.recommended_contrast, `${pair.text} on ${pair.background}`);
  }
});
