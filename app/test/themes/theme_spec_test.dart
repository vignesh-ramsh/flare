import 'package:app/app/themes/theme_registry.dart';
import 'package:app/app/themes/theme_spec.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'theme_fixtures.dart';

Map<String, Object?> json({
  Object? name = 'Midnight',
  Object? brightness = 'dark',
  Object? colors = const {'primary': '#8AB4F8'},
}) => {'name': name, 'brightness': brightness, 'colors': colors};

void main() {
  group('the colours a theme can set', () {
    test('every one has its own file name, in lower snake case', () {
      final keys = ThemeToken.values.map((t) => t.key).toList();
      expect(keys.toSet(), hasLength(keys.length));
      for (final key in keys) {
        expect(key, matches(RegExp(r'^[a-z]+(_[a-z]+)*$')));
      }
    });

    test('include the ones a theme author expects to find', () {
      expect(
        ThemeToken.values.map((t) => t.key),
        containsAll([
          'primary',
          'background',
          'surface',
          'border',
          'on_surface',
          'error',
          'shadow',
        ]),
      );
    });

    test('are found by their name, and only by that', () {
      expect(ThemeToken.fromKey('border'), ThemeToken.border);
      expect(ThemeToken.fromKey('on_primary'), ThemeToken.onPrimary);
      expect(ThemeToken.fromKey('onPrimary'), isNull);
      expect(ThemeToken.fromKey('nope'), isNull);
    });
  });

  group('colours as text', () {
    test('read #RRGGBB in either case', () {
      expect(parseHexColor('#8AB4F8'), const Color(0xFF8AB4F8));
      expect(parseHexColor('#8ab4f8'), const Color(0xFF8AB4F8));
      expect(parseHexColor(' #000000 '), const Color(0xFF000000));
    });

    test('refuse anything else', () {
      for (final text in [
        '',
        '8AB4F8',
        '#8AB4F',
        '#8AB4F80',
        '#GGGGGG',
        'red',
        'rgb(1,2,3)',
        '#8AB4F8FF',
      ]) {
        expect(parseHexColor(text), isNull, reason: text);
      }
    });

    test('are written in upper case with the hash', () {
      expect(formatHexColor(const Color(0xFF0A0B0C)), '#0A0B0C');
      expect(formatHexColor(const Color(0xFF8AB4F8)), '#8AB4F8');
    });
  });

  group('a theme\'s name', () {
    test('may use letters, digits, spaces, dots, dashes and underscores', () {
      for (final name in [
        'Midnight',
        'High contrast 2',
        'a',
        'v1.2-beta_x',
        'A' * 32,
      ]) {
        expect(ThemeSpec.isValidName(name), isTrue, reason: name);
      }
    });

    test('may not be empty, long, start with a symbol, or hold others', () {
      for (final name in [
        '',
        ' lead',
        '-dash',
        'A' * 33,
        'semi;colon',
        'colon:name',
        'slash/name',
        'emoji😀',
        'new\nline',
      ]) {
        expect(ThemeSpec.isValidName(name), isFalse, reason: name);
      }
    });

    test('is the same whatever its case', () {
      expect(paper().nameKey, 'paper');
      expect(
        ThemeSpec.seeded(
          name: 'PAPER',
          brightness: Brightness.light,
          primary: Colors.blue,
        ).nameKey,
        'paper',
      );
    });
  });

  group('reading a theme', () {
    test('takes the name, brightness and colours', () {
      final spec = ThemeSpec.fromJson(
        json(
          colors: {
            'primary': '#8AB4F8',
            'background': '#0B1220',
            'border': '#334155',
          },
        ),
      );
      expect(spec.name, 'Midnight');
      expect(spec.brightness, Brightness.dark);
      expect(spec.colors, {
        ThemeToken.primary: const Color(0xFF8AB4F8),
        ThemeToken.background: const Color(0xFF0B1220),
        ThemeToken.border: const Color(0xFF334155),
      });
    });

    test('reads light too', () {
      expect(
        ThemeSpec.fromJson(json(brightness: 'light')).brightness,
        Brightness.light,
      );
    });

    test('ignores a colour this version does not know, so a newer theme still opens', () {
      final spec = ThemeSpec.fromJson(
        json(colors: {'primary': '#8AB4F8', 'sparkle': '#FFFFFF'}),
      );
      expect(spec.colors.keys, [ThemeToken.primary]);
    });

    test('needs a primary colour, which the rest derive from', () {
      expect(
        () => ThemeSpec.fromJson(json(colors: {'background': '#000000'})),
        throwsA(
          isA<ThemeFormatException>().having(
            (e) => e.message,
            'message',
            contains('primary is required'),
          ),
        ),
      );
    });

    test('says which colour is wrong', () {
      expect(
        () => ThemeSpec.fromJson(
          json(colors: {'primary': '#8AB4F8', 'border': 'grey'}),
        ),
        throwsA(
          isA<ThemeFormatException>().having(
            (e) => e.message,
            'message',
            allOf(contains('border'), contains('grey')),
          ),
        ),
      );
      expect(
        () => ThemeSpec.fromJson(json(colors: {'primary': 12345})),
        throwsA(isA<ThemeFormatException>()),
      );
    });

    test(
      'refuses a bad name or brightness, or a shape that is not a theme',
      () {
        for (final bad in <Object?>[
          json(name: ''),
          json(name: 'bad;name'),
          json(name: 42),
          json(name: null),
          json(brightness: 'dim'),
          json(brightness: null),
          json(colors: null),
          json(colors: ['primary']),
          'a string',
          null,
          42,
          <Object?>[],
        ]) {
          expect(
            () => ThemeSpec.fromJson(bad),
            throwsA(isA<ThemeFormatException>()),
            reason: '$bad',
          );
        }
      },
    );
  });

  group('writing a theme', () {
    test(
      'gives back what was read, colours in the order of the list of colours',
      () {
        final original = fullTheme();
        final again = ThemeSpec.fromJson(original.toJson());
        expect(again, original);
        expect(again.hashCode, original.hashCode);
        expect(
          (original.toJson()['colors']! as Map).keys.toList(),
          ThemeToken.values.map((t) => t.key).toList(),
        );
      },
    );

    test('writes only the colours it sets', () {
      expect((midnight().toJson()['colors']! as Map).keys, [
        'primary',
        'background',
        'surface',
        'on_surface',
      ]);
    });

    test('names the brightness', () {
      expect(midnight().toJson()['brightness'], 'dark');
      expect(paper().toJson()['brightness'], 'light');
    });
  });

  test('two themes are equal when everything about them is', () {
    expect(paper() == paper(), isTrue);
    expect(paper() == midnight(), isFalse);
    expect(
      ThemeSpec.seeded(
            name: 'Paper',
            brightness: Brightness.dark,
            primary: const Color(0xFF455A64),
          ) ==
          paper(),
      isFalse,
    );
  });

  group('contrast', () {
    test(
      'is 21 between black and white, and 1 between a colour and itself',
      () {
        expect(contrastRatio(Colors.black, Colors.white), closeTo(21, 0.01));
        expect(contrastRatio(Colors.white, Colors.black), closeTo(21, 0.01));
        expect(contrastRatio(Colors.red, Colors.red), 1);
      },
    );
  });

  group('which theme', () {
    test('a built-in one has an id from its name in lower case', () {
      expect(const ThemeRef.builtin('Midnight').id, 'builtin:midnight');
    });

    test('a server\'s has an id from its host and name', () {
      expect(
        const ThemeRef.server('example.com', 'Midnight').id,
        'server:example.com:midnight',
      );
    });

    test('is read back from its id, including a host with a port', () {
      expect(
        ThemeRef.parse('builtin:midnight'),
        const ThemeRef.builtin('midnight'),
      );
      expect(
        ThemeRef.parse('server:example.com:midnight'),
        const ThemeRef.server('example.com', 'midnight'),
      );
      final withPort = ThemeRef.parse('server:10.0.0.2:8080:midnight')!;
      expect(withPort.serverHost, '10.0.0.2:8080');
      expect(withPort.name, 'midnight');
    });

    test('is the same whatever the case of the name', () {
      expect(
        const ThemeRef.builtin('Midnight'),
        ThemeRef.parse('builtin:midnight'),
      );
    });

    test('is nothing for what is not an id', () {
      for (final text in [
        null,
        '',
        'system',
        'builtin:',
        'server:',
        'server:host',
        'server:host:',
        'server::name',
        'other:x',
      ]) {
        expect(ThemeRef.parse(text), isNull, reason: '$text');
      }
    });
  });
}
