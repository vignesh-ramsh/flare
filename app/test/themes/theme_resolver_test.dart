import 'package:app/app/theme.dart';
import 'package:app/app/themes/theme_registry.dart';
import 'package:app/app/themes/theme_resolver.dart';
import 'package:app/app/themes/theme_spec.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'theme_fixtures.dart';

void main() {
  group('colours a theme sets', () {
    test('are used exactly, every one of them', () {
      final spec = fullTheme();
      final resolved = resolveTheme(spec);
      for (final token in ThemeToken.values) {
        expect(
          resolved[token],
          spec.colors[token],
          reason: '${token.key} is ${formatHexColor(resolved[token])}',
        );
      }
    });

    test('reach the places the app reads them from', () {
      final spec = fullTheme();
      final theme = buildTheme(Brightness.light, spec: spec);
      final c = spec.colors;
      expect(theme.colorScheme.primary, c[ThemeToken.primary]);
      expect(theme.colorScheme.surface, c[ThemeToken.surface]);
      expect(theme.colorScheme.outline, c[ThemeToken.border]);
      expect(theme.colorScheme.outlineVariant, c[ThemeToken.borderSubtle]);
      expect(theme.colorScheme.onSurface, c[ThemeToken.onSurface]);
      expect(theme.colorScheme.error, c[ThemeToken.error]);
      expect(theme.scaffoldBackgroundColor, c[ThemeToken.background]);
      expect(theme.cardTheme.color, c[ThemeToken.surfaceContainerLow]);
      expect(theme.dividerTheme.color, c[ThemeToken.borderSubtle]);
    });

    test('keep a background that differs from the surface', () {
      final resolved = resolveTheme(fullTheme());
      expect(
        resolved[ThemeToken.background],
        isNot(resolved[ThemeToken.surface]),
      );
    });
  });

  group('colours a theme leaves out', () {
    test(
      'come from the palette for its primary, so one colour is a whole theme',
      () {
        final resolved = resolveTheme(paper());
        expect(resolved[ThemeToken.primary], const Color(0xFF455A64));
        final seeded = ColorScheme.fromSeed(
          seedColor: const Color(0xFF455A64),
          brightness: Brightness.light,
        );
        expect(resolved[ThemeToken.surface], seeded.surface);
        expect(resolved[ThemeToken.border], seeded.outline);
        expect(resolved[ThemeToken.secondary], seeded.secondary);
        expect(resolved[ThemeToken.error], seeded.error);
      },
    );

    test('follow the brightness', () {
      final light = resolveTheme(
        ThemeSpec.seeded(
          name: 'L',
          brightness: Brightness.light,
          primary: Colors.teal,
        ),
      );
      final dark = resolveTheme(
        ThemeSpec.seeded(
          name: 'D',
          brightness: Brightness.dark,
          primary: Colors.teal,
        ),
      );
      expect(light[ThemeToken.surface].computeLuminance(), greaterThan(0.5));
      expect(dark[ThemeToken.surface].computeLuminance(), lessThan(0.1));
    });

    test('use the surface as the page when there is no background', () {
      final resolved = resolveTheme(paper());
      expect(resolved[ThemeToken.background], resolved[ThemeToken.surface]);
    });

    test('are readable text on a colour the theme did set (a yellow button gets dark text)', () {
      final yellow = resolveTheme(
        ThemeSpec.seeded(
          name: 'Y',
          brightness: Brightness.light,
          primary: const Color(0xFFFACC15),
        ),
      );
      expect(yellow[ThemeToken.primary], const Color(0xFFFACC15));
      expect(
        contrastRatio(yellow[ThemeToken.onPrimary], yellow[ThemeToken.primary]),
        greaterThanOrEqualTo(4.5),
      );
      final navy = resolveTheme(
        ThemeSpec.seeded(
          name: 'N',
          brightness: Brightness.light,
          primary: const Color(0xFF0A1A3A),
        ),
      );
      expect(navy[ThemeToken.onPrimary], Colors.white);
    });

    test('are readable text on a surface the theme did set, and layers stepped from it', () {
      final resolved = resolveTheme(midnight());
      expect(resolved[ThemeToken.surface], const Color(0xFF111A2B));
      expect(resolved[ThemeToken.onSurface], const Color(0xFFE6EDF7));
      expect(
        contrastRatio(
          resolved[ThemeToken.onSurfaceVariant],
          resolved[ThemeToken.surface],
        ),
        greaterThanOrEqualTo(4.5),
      );
      // Each layer is a little further from the surface than the one before.
      final layers =
          [
                ThemeToken.surfaceContainerLow,
                ThemeToken.surfaceContainer,
                ThemeToken.surfaceContainerHigh,
                ThemeToken.surfaceContainerHighest,
              ]
              .map(
                (t) => contrastRatio(resolved[t], resolved[ThemeToken.surface]),
              )
              .toList();
      expect(layers, orderedEquals([...layers]..sort()));
      expect(layers.first, greaterThan(1));
    });

    test('are all decided, whatever little the theme says', () {
      for (final spec in [paper(), midnight(), fullTheme()]) {
        final resolved = resolveTheme(spec);
        for (final token in ThemeToken.values) {
          expect(
            resolved[token].a,
            1,
            reason: '${spec.name} ${token.key} is opaque',
          );
        }
      }
    });
  });

  group('readability', () {
    test('a theme that sets everything well has no problems', () {
      expect(resolveTheme(fullTheme()).readabilityProblems(), isEmpty);
    });

    test(
      'a theme of one colour has none, for any colour at all, light or dark',
      () {
        final problems = <String>[];
        for (final brightness in Brightness.values) {
          for (var hue = 0; hue < 360; hue += 15) {
            for (final (saturation, value) in [
              (1.0, 1.0),
              (1.0, 0.5),
              (0.5, 0.9),
              (0.15, 0.3),
              (0.0, 0.0),
              (0.0, 1.0),
              (0.0, 0.5),
            ]) {
              final color = HSVColor.fromAHSV(
                1,
                hue.toDouble(),
                saturation,
                value,
              ).toColor();
              final spec = ThemeSpec.seeded(
                name: 'S',
                brightness: brightness,
                primary: color,
              );
              for (final p in resolveTheme(spec).readabilityProblems()) {
                problems.add('${brightness.name} ${formatHexColor(color)}: $p');
              }
            }
          }
        }
        expect(problems, isEmpty);
      },
    );

    test('finds text that cannot be read, and says which', () {
      final problems = resolveTheme(unreadable()).readabilityProblems();
      expect(problems, isNotEmpty);
      expect(problems.map((p) => p.text), contains(ThemeToken.onSurface));
      expect(problems.every((p) => p.ratio < minimumContrast), isTrue);
      expect(problems.first.toString(), contains('on_surface on surface'));
    });

    test('can be asked for a higher bar', () {
      final resolved = resolveTheme(midnight());
      expect(resolved.readabilityProblems(minimum: 30), isNotEmpty);
    });

    test('every pair that must be readable names two colours', () {
      expect(readablePairs.toSet(), hasLength(readablePairs.length));
      for (final (text, behind) in readablePairs) {
        expect(text, isNot(behind));
      }
    });
  });

  group('the themes on offer', () {
    test('are the built-in ones, then each server\'s', () {
      final themes = collectThemes(
        builtin: [paper(), midnight()],
        servers: {
          'example.com': [fullTheme()],
        },
      );
      expect(themes.map((t) => t.ref.id), [
        'builtin:paper',
        'builtin:midnight',
        'server:example.com:full',
      ]);
      expect(themes.last.spec, fullTheme());
    });

    test('leave out a theme nobody could read', () {
      final themes = collectThemes(builtin: [paper(), unreadable()]);
      expect(themes.map((t) => t.name), ['Paper']);
    });

    test(
      'keep the first of a name used twice in one place, whatever the case',
      () {
        final other = ThemeSpec.seeded(
          name: 'PAPER',
          brightness: Brightness.dark,
          primary: Colors.red,
        );
        final themes = collectThemes(builtin: [paper(), other]);
        expect(themes, hasLength(1));
        expect(themes.single.spec, paper());
      },
    );

    test('allow the same name in different places, as different themes', () {
      final themes = collectThemes(
        builtin: [paper()],
        servers: {
          'a.example': [paper()],
          'b.example': [paper()],
        },
      );
      expect(themes.map((t) => t.ref.id), [
        'builtin:paper',
        'server:a.example:paper',
        'server:b.example:paper',
      ]);
    });

    test('are none when there are none', () {
      expect(collectThemes(builtin: const []), isEmpty);
    });
  });

  group('the default theme', () {
    test('keeps the brand colour for the logo and the unread badge', () {
      final theme = buildTheme(Brightness.light);
      expect(theme.extension<PlatformTokens>()!.accent, brandColor);
    });

    test(
      'has the accent of a chosen theme, so the badge belongs to its palette',
      () {
        final theme = buildTheme(Brightness.dark, spec: midnight());
        expect(
          theme.extension<PlatformTokens>()!.accent,
          const Color(0xFF8AB4F8),
        );
        expect(theme.colorScheme.brightness, Brightness.dark);
      },
    );

    test(
      'is equal to itself when built again, so rebuilding it does not animate',
      () {
        expect(
          buildTheme(Brightness.light) == buildTheme(Brightness.light),
          isTrue,
        );
        expect(
          buildTheme(Brightness.dark, spec: midnight()) ==
              buildTheme(Brightness.dark, spec: midnight()),
          isTrue,
        );
        expect(
          buildTheme(Brightness.dark, spec: midnight()) ==
              buildTheme(Brightness.dark),
          isFalse,
        );
        expect(
          const PlatformTokens(targetSize: 40).hashCode,
          const PlatformTokens(targetSize: 40).hashCode,
        );
        expect(
          const PlatformTokens(targetSize: 40) ==
              const PlatformTokens(targetSize: 48),
          isFalse,
        );
      },
    );

    test('is the same as before themes existed', () {
      final seeded = ColorScheme.fromSeed(
        seedColor: brandColor,
        brightness: Brightness.light,
      );
      final theme = buildTheme(Brightness.light);
      expect(theme.colorScheme, seeded);
      expect(theme.scaffoldBackgroundColor, seeded.surface);
    });
  });
}
