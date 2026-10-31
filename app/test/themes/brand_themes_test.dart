import 'dart:convert';
import 'dart:io';

import 'package:app/app/theme.dart';
import 'package:app/app/themes/theme_providers.dart';
import 'package:app/app/themes/theme_resolver.dart';
import 'package:app/app/themes/theme_spec.dart';
import 'package:app/brand/themes.g.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'theme_fixtures.dart';

Map<String, dynamic> get contract =>
    jsonDecode(File('../api/theme-tokens.json').readAsStringSync())
        as Map<String, dynamic>;

void main() {
  group('the shared list of colours (api/theme-tokens.json)', () {
    test('has the colours the app knows, in the same order', () {
      expect(ThemeToken.values.map((t) => t.key), [
        for (final t in contract['tokens'] as List) (t as Map)['key'],
      ]);
    });

    test('has the same text-on-background pairs', () {
      expect(readablePairs.map((p) => '${p.$1.key}/${p.$2.key}'), [
        for (final pair in contract['readable_pairs'] as List)
          '${(pair as Map)['text']}/${pair['background']}',
      ]);
    });

    test('has the same contrast rules and the same name and colour rules', () {
      expect(minimumContrast, contract['minimum_contrast']);
      expect(recommendedContrast, contract['recommended_contrast']);
      expect(ThemeSpec.namePattern.pattern, contract['name_pattern']);
      expect(contract['color_pattern'], r'^#[0-9A-Fa-f]{6}$');
      expect(contract['required_tokens'], ['primary']);
    });

    test('puts every colour in a group, each group in the list', () {
      final groups = {
        for (final g in contract['groups'] as List) (g as Map)['id'],
      };
      for (final t in contract['tokens'] as List) {
        expect(groups, contains((t as Map)['group']), reason: '${t['key']}');
        expect(t['label'], isNotEmpty);
        expect(t['help'], isNotEmpty);
      }
    });
  });

  group('the themes built into this app (from the brand folder)', () {
    final themes = parseBrandThemes(brandThemes);

    test('all read, none left out', () {
      expect(brandThemes, isNotEmpty);
      expect(themes, hasLength(brandThemes.length));
    });

    test('have unique names, whatever their case', () {
      final names = themes.map((t) => t.nameKey).toList();
      expect(names.toSet(), hasLength(names.length));
    });

    test('are readable, at the level WCAG asks for ordinary text', () {
      for (final spec in themes) {
        expect(
          resolveTheme(spec).readabilityProblems(minimum: recommendedContrast),
          isEmpty,
          reason: spec.name,
        );
      }
    });

    test('are all of light and dark', () {
      expect(themes.map((t) => t.brightness).toSet(), {
        Brightness.light,
        Brightness.dark,
      });
    });

    test('each build into a theme the app can use', () {
      for (final spec in themes) {
        final theme = buildTheme(spec.brightness, spec: spec);
        expect(
          theme.colorScheme.brightness,
          spec.brightness,
          reason: spec.name,
        );
        expect(
          theme.colorScheme.primary,
          spec.colors[ThemeToken.primary],
          reason: spec.name,
        );
      }
    });

    test('a theme that cannot be read is left out, not fatal', () {
      final parsed = parseBrandThemes([
        {
          'name': 'Good',
          'brightness': 'light',
          'colors': {'primary': '#112233'},
        },
        {
          'name': 'bad;name',
          'brightness': 'light',
          'colors': {'primary': '#112233'},
        },
        {
          'name': 'NoPrimary',
          'brightness': 'dark',
          'colors': <String, Object>{},
        },
      ]);
      expect(parsed.map((t) => t.name), ['Good']);
    });
  });

  group('following the system', () {
    ProviderContainer container({
      List<ThemeSpec>? builtin,
      String light = '',
      String dark = '',
    }) {
      final c = ProviderContainer(
        overrides: [
          builtinThemesProvider.overrideWithValue(
            builtin ?? [paper(), midnight()],
          ),
          brandDefaultNamesProvider.overrideWithValue((
            light: light,
            dark: dark,
          )),
          themeChoiceProvider.overrideWithValue(const AsyncData(null)),
        ],
      );
      addTearDown(c.dispose);
      return c;
    }

    test('uses colours derived from the brand colour when the brand names no default', () {
      final theme = container().read(appThemeProvider);
      expect(theme.mode, ThemeMode.system);
      expect(theme.active, isNull);
      expect(theme.light.colorScheme, buildTheme(Brightness.light).colorScheme);
      expect(theme.dark.colorScheme, buildTheme(Brightness.dark).colorScheme);
    });

    test('uses the brand\'s own light and dark themes when it names them', () {
      final theme = container(
        light: 'Paper',
        dark: 'Midnight',
      ).read(appThemeProvider);
      expect(theme.mode, ThemeMode.system);
      expect(theme.light.colorScheme.primary, const Color(0xFF455A64));
      expect(theme.dark.colorScheme.primary, const Color(0xFF8AB4F8));
      expect(theme.dark.scaffoldBackgroundColor, const Color(0xFF0B1220));
      expect(theme.active, isNull, reason: 'the user has not chosen one');
    });

    test('matches the names without regard to case', () {
      final theme = container(light: ' PAPER ').read(appThemeProvider);
      expect(theme.light.colorScheme.primary, const Color(0xFF455A64));
    });

    test(
      'ignores a default of the wrong brightness, or one that is not there',
      () {
        final theme = container(
          light: 'Midnight',
          dark: 'Nope',
        ).read(appThemeProvider);
        expect(
          theme.light.colorScheme,
          buildTheme(Brightness.light).colorScheme,
        );
        expect(theme.dark.colorScheme, buildTheme(Brightness.dark).colorScheme);
      },
    );

    test('does not take a default from a server\'s themes', () {
      final c = ProviderContainer(
        overrides: [
          builtinThemesProvider.overrideWithValue(const []),
          serverThemesProvider.overrideWithValue({
            'example.com': [paper()],
          }),
          brandDefaultNamesProvider.overrideWithValue((
            light: 'Paper',
            dark: '',
          )),
          themeChoiceProvider.overrideWithValue(const AsyncData(null)),
        ],
      );
      addTearDown(c.dispose);
      expect(
        c.read(appThemeProvider).light.colorScheme,
        buildTheme(Brightness.light).colorScheme,
      );
    });

    test('falls back to those defaults when a chosen theme goes away', () {
      final c = ProviderContainer(
        overrides: [
          builtinThemesProvider.overrideWithValue([paper(), midnight()]),
          brandDefaultNamesProvider.overrideWithValue((
            light: 'Paper',
            dark: 'Midnight',
          )),
          themeChoiceProvider.overrideWithValue(
            const AsyncData('server:gone.example:x'),
          ),
        ],
      );
      addTearDown(c.dispose);
      final theme = c.read(appThemeProvider);
      expect(theme.mode, ThemeMode.system);
      expect(theme.light.colorScheme.primary, const Color(0xFF455A64));
    });
  });
}
