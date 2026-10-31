import 'dart:convert';
import 'dart:io';

import 'package:brand_tool/brand_tool.dart';
import 'package:path/path.dart' as p;
import 'package:test/test.dart';

import 'support.dart';

/// A theme that sets only what is given; `primary` is added unless [colors] has it.
String theme({
  String name = 'Midnight',
  String brightness = 'dark',
  Map<String, String> colors = const {},
  bool primary = true,
}) {
  final all = {if (primary) 'primary': '#8AB4F8', ...colors};
  final lines = all.entries
      .map((e) => '      ${e.key}: "${e.value}"')
      .join('\n');
  return '  - name: $name\n    brightness: $brightness\n    colors:\n$lines\n';
}

String file(String themes, {String defaults = '', String extra = ''}) =>
    'schema: 1\n$defaults${extra}themes:\n$themes';

(BrandThemes?, Report) parse(String yaml) {
  final report = Report();
  return (parseThemesYaml(yaml, report), report);
}

Map<String, dynamic> get contract =>
    jsonDecode(
          File(
            p.join(repoRoot.path, 'api', 'theme-tokens.json'),
          ).readAsStringSync(),
        )
        as Map<String, dynamic>;

void main() {
  group('the shared list of colours', () {
    test('is the one in api/theme-tokens.json, in the same order', () {
      expect(themeTokenKeys, [
        for (final t in contract['tokens'] as List) (t as Map)['key'],
      ]);
    });

    test('has the same text-on-background pairs', () {
      expect(themeReadablePairs.map((p) => '${p.$1}/${p.$2}'), [
        for (final pair in contract['readable_pairs'] as List)
          '${(pair as Map)['text']}/${pair['background']}',
      ]);
    });

    test('has the same minimum contrast, name rule and colour rule', () {
      expect(themeMinimumContrast, contract['minimum_contrast']);
      expect(maxBrandThemes, greaterThan(0));
      const good = ['Midnight', 'a', 'High contrast 2', 'v1.2-beta_x'];
      const bad = ['', ' lead', '-dash', 'semi;colon', 'colon:name'];
      final name = RegExp(contract['name_pattern'] as String);
      for (final n in good) {
        expect(name.hasMatch(n), isTrue, reason: n);
        expect(parse(file(theme(name: n))).$2.errors, isEmpty, reason: n);
      }
      for (final n in bad) {
        expect(name.hasMatch(n), isFalse, reason: n);
        expect(
          parse(file(theme(name: n.isEmpty ? '""' : '"$n"'))).$1,
          isNull,
          reason: n,
        );
      }
      expect(
        RegExp(contract['color_pattern'] as String).hasMatch('#8AB4F8'),
        isTrue,
      );
    });
  });

  group('the official brand and the example', () {
    for (final entry in {'official': flareBrand, 'acme': acmeBrand}.entries) {
      test('${entry.key}: themes are valid, with no warnings', () {
        final (brand, report) = load(entry.value);
        expect(report.errors, isEmpty);
        expect(report.warnings, isEmpty);
        expect(brand!.themes.themes, isNotEmpty);
      });

      test(
        '${entry.key}: every theme sets every colour, in order, readable at 4.5 to 1',
        () {
          final themes = loadValid(entry.value).themes.themes;
          for (final t in themes) {
            expect(t.colors.keys.toList(), themeTokenKeys, reason: t.name);
            for (final (text, behind) in themeReadablePairs) {
              expect(
                contrastOf(t.colors[text]!, t.colors[behind]!),
                greaterThanOrEqualTo(4.5),
                reason: '${t.name}: $text on $behind',
              );
            }
          }
        },
      );

      test('${entry.key}: names are unique whatever their case', () {
        final names = loadValid(
          entry.value,
        ).themes.themes.map((t) => t.name.toLowerCase()).toList();
        expect(names.toSet(), hasLength(names.length));
      });
    }

    test(
      'the official brand offers light and dark themes, and uses the derived default',
      () {
        final themes = loadValid(flareBrand).themes;
        expect(themes.themes.map((t) => t.brightness).toSet(), {
          'light',
          'dark',
        });
        expect(themes.defaultLight, isEmpty);
        expect(themes.defaultDark, isEmpty);
      },
    );

    test('acme names its own defaults', () {
      final themes = loadValid(acmeBrand).themes;
      expect(themes.defaultLight, 'Acme Light');
      expect(themes.defaultDark, 'Acme Dark');
    });
  });

  group('reading a themes file', () {
    test(
      'takes the name, brightness and colours, upper-casing the colours and ordering them',
      () {
        final (themes, report) = parse(
          file(theme(colors: {'border': '#334155', 'background': '#0b1220'})),
        );
        expect(report.errors, isEmpty);
        final t = themes!.themes.single;
        expect(t.name, 'Midnight');
        expect(t.brightness, 'dark');
        expect(t.colors, {
          'primary': '#8AB4F8',
          'background': '#0B1220',
          'border': '#334155',
        });
        expect(t.colors.keys.toList(), ['primary', 'background', 'border']);
      },
    );

    test('an empty file, or one with no themes, is no themes', () {
      expect(parse('').$1!.themes, isEmpty);
      expect(parse('schema: 1\n').$1!.themes, isEmpty);
      expect(parse('schema: 1\nthemes: []\n').$1!.themes, isEmpty);
    });

    test('a theme of one colour is enough', () {
      expect(parse(file(theme())).$2.errors, isEmpty);
    });

    group('refuses', () {
      void refused(String why, String yaml, String expected) {
        test(why, () {
          final (themes, report) = parse(yaml);
          expect(themes, isNull);
          expect(report.errors.join('\n'), contains(expected));
        });
      }

      refused('YAML that is not YAML', 'themes: [unclosed', 'not valid YAML');
      refused(
        'a file that is not a mapping',
        '- just\n- a list\n',
        'must contain a mapping',
      );
      refused('another schema', 'schema: 2\nthemes: []\n', 'must be 1');
      refused(
        'a setting it does not know',
        'schema: 1\nstuff: 1\nthemes: []\n',
        'unknown setting "stuff"',
      );
      refused(
        'themes that are not a list',
        'schema: 1\nthemes: nope\n',
        'must be a list',
      );
      refused(
        'a theme that is not a mapping',
        'schema: 1\nthemes:\n  - just a string\n',
        'must be a mapping',
      );
      refused(
        'a bad name',
        file(theme(name: '"bad;name"')),
        'name must be 1 to 32',
      );
      refused(
        'a missing name',
        file('  - brightness: dark\n    colors:\n      primary: "#8AB4F8"\n'),
        'name must be 1 to 32',
      );
      refused(
        'a bad brightness',
        file(theme(brightness: 'dim')),
        'brightness must be light or dark',
      );
      refused(
        'an unknown setting in a theme',
        file('${theme()}    sparkle: yes\n'),
        'unknown setting "sparkle"',
      );
      refused(
        'colours that are not a mapping',
        file('  - name: X\n    brightness: dark\n    colors: red\n'),
        'colors must list',
      );
      refused(
        'a colour a theme cannot set',
        file(theme(colors: {'sparkle': '#FFFFFF'})),
        '"sparkle" is not a colour',
      );
      refused(
        'a camelCase colour name',
        file(theme(colors: {'onPrimary': '#FFFFFF'})),
        '"onPrimary" is not a colour',
      );
      refused(
        'a colour that is not #RRGGBB',
        file(theme(colors: {'border': 'grey'})),
        'colors.border: must be a colour like "#RRGGBB"',
      );
      refused(
        'a short colour',
        file(theme(colors: {'border': '#FFF'})),
        'colors.border',
      );
      refused(
        'a colour with alpha',
        file(theme(colors: {'border': '#FFFFFFFF'})),
        'colors.border',
      );
      refused(
        'an unquoted colour (YAML reads it as a comment)',
        file(
          '  - name: X\n    brightness: dark\n    colors:\n      primary: #8AB4F8\n',
        ),
        'colors.primary',
      );
      refused(
        'a theme with no primary',
        file(theme(primary: false, colors: {'background': '#000000'})),
        'colors.primary is required',
      );
      refused(
        'a name used twice',
        file('${theme()}${theme(brightness: 'light')}'),
        'already used',
      );
      refused(
        'a name used twice in another case',
        file('${theme()}${theme(name: 'MIDNIGHT')}'),
        'already used by "Midnight"',
      );
      refused(
        'text that cannot be read on its background',
        file(theme(colors: {'surface': '#808080', 'on_surface': '#858585'})),
        'on_surface (#858585) on surface (#808080)',
      );
      refused(
        'too many themes',
        file(
          List.generate(maxBrandThemes + 1, (i) => theme(name: 'T$i')).join(),
        ),
        'at most $maxBrandThemes',
      );
    });

    test('says which theme an error is in', () {
      final (_, report) = parse(
        file('${theme()}${theme(name: 'Broken', colors: {'border': 'grey'})}'),
      );
      expect(report.errors.single, contains('theme "Broken"'));
    });

    test(
      'lets a pair through when only one of its colours is set, since the rest is derived by the app',
      () {
        expect(
          parse(file(theme(colors: {'on_surface': '#FFFFFF'}))).$2.errors,
          isEmpty,
        );
      },
    );

    test('accepts text at exactly the minimum, and refuses just under it', () {
      // #757575 on white is 4.6:1; #949494 is 3.03:1; #9E9E9E is 2.68:1.
      expect(
        parse(
          file(theme(colors: {'surface': '#FFFFFF', 'on_surface': '#949494'})),
        ).$2.errors,
        isEmpty,
      );
      expect(
        parse(
          file(theme(colors: {'surface': '#FFFFFF', 'on_surface': '#9E9E9E'})),
        ).$2.errors,
        isNotEmpty,
      );
    });

    test('warns about a background without a surface', () {
      final (themes, report) = parse(
        file(theme(colors: {'background': '#0B1220'})),
      );
      expect(themes, isNotNull);
      expect(
        report.warnings.single,
        contains('sets background but not surface'),
      );
    });
  });

  group('the default themes', () {
    final two = theme(name: 'Day', brightness: 'light') + theme(name: 'Night');

    test('are named, matched without regard to case', () {
      final (themes, report) = parse(
        file(two, defaults: 'defaults:\n  light: day\n  dark: " Night "\n'),
      );
      expect(report.errors, isEmpty);
      expect(themes!.defaultLight, 'Day');
      expect(themes.defaultDark, 'Night');
    });

    test('may be left empty or out', () {
      expect(
        parse(file(two, defaults: 'defaults:\n  light: ""\n')).$1!.defaultLight,
        isEmpty,
      );
      expect(parse(file(two)).$1!.defaultDark, isEmpty);
    });

    test('must name a theme in the list', () {
      final (themes, report) = parse(
        file(two, defaults: 'defaults:\n  light: Nope\n'),
      );
      expect(themes, isNull);
      expect(
        report.errors.single,
        contains('"Nope", which is not in the list'),
      );
    });

    test('must have the right brightness', () {
      final (themes, report) = parse(
        file(two, defaults: 'defaults:\n  light: Night\n'),
      );
      expect(themes, isNull);
      expect(report.errors.single, contains('"Night" is a dark theme'));
    });

    test('allow only light and dark', () {
      expect(parse(file(two, defaults: 'defaults:\n  dim: Day\n')).$1, isNull);
      expect(parse(file(two, defaults: 'defaults: Day\n')).$1, isNull);
    });
  });

  group('loading a brand with themes', () {
    Directory withThemes(String? themesYaml, {String key = 'themes.yaml'}) {
      final dir = copyBrand(
        flareBrand,
        edits: [swap('  themes: themes.yaml', '  themes: $key')],
      );
      if (themesYaml != null) {
        File(p.join(dir.path, 'themes.yaml')).writeAsStringSync(themesYaml);
      }
      return dir;
    }

    test('reads the file the brand names', () {
      final brand = loadValid(withThemes(file(theme(name: 'Only'))));
      expect(brand.themes.themes.map((t) => t.name), ['Only']);
    });

    test('a brand that names no themes file has no themes', () {
      final dir = copyBrand(
        flareBrand,
        edits: [swap('  themes: themes.yaml', '')],
      );
      expect(loadValid(dir).themes.themes, isEmpty);
    });

    test('a file that is not there is an error', () {
      final dir = withThemes(null);
      File(p.join(dir.path, 'themes.yaml')).deleteSync();
      final (brand, report) = load(dir);
      expect(brand, isNull);
      expect(report.errors.join(), contains('look.themes: file not found'));
    });

    test('a file outside the brand folder is refused', () {
      for (final key in ['/etc/passwd', '../themes.yaml']) {
        final (brand, report) = load(withThemes(null, key: key));
        expect(brand, isNull, reason: key);
        expect(report.errors, isNotEmpty, reason: key);
      }
    });

    test('a bad theme stops the brand, and the error names the file', () {
      final (brand, report) = load(
        withThemes(file(theme(colors: {'border': 'grey'}))),
      );
      expect(brand, isNull);
      expect(report.errors.join(), contains('themes.yaml theme "Midnight"'));
    });
  });

  group('what is generated', () {
    String generated(Directory brand) {
      final out = renderBrand(loadValid(brand));
      return utf8.decode(out.files['lib/brand/themes.g.dart']!);
    }

    test('holds every theme and colour of the official brand', () {
      final dart = generated(flareBrand);
      final themes = loadValid(flareBrand).themes.themes;
      for (final t in themes) {
        expect(dart, contains("'name': '${t.name}'"));
        for (final e in t.colors.entries) {
          expect(dart, contains("'${e.key}': '${e.value}'"));
        }
      }
      expect(dart, contains("const String brandDefaultLightTheme = '';"));
    });

    test(
      'holds the example\'s own themes and defaults, and none of the official ones',
      () {
        final dart = generated(acmeBrand);
        expect(dart, contains("'name': 'Acme Light'"));
        expect(
          dart,
          contains("const String brandDefaultLightTheme = 'Acme Light';"),
        );
        expect(
          dart,
          contains("const String brandDefaultDarkTheme = 'Acme Dark';"),
        );
        for (final t in loadValid(flareBrand).themes.themes) {
          expect(dart, isNot(contains("'name': '${t.name}'")));
        }
      },
    );

    test(
      'is an empty list for a brand without themes, and is still written',
      () {
        final dir = copyBrand(
          flareBrand,
          edits: [swap('  themes: themes.yaml', '')],
        );
        final dart = generated(dir);
        expect(dart, contains('brandThemes = <Map<String, Object>>[\n\n];'));
      },
    );

    test('is the same every time', () {
      expect(generated(flareBrand), generated(flareBrand));
    });

    test('is among the files the generator owns', () {
      expect(ownedFiles, contains('lib/brand/themes.g.dart'));
    });

    test('is ignored by git, like the other generated files', () {
      final ignore = File(
        p.join(repoRoot.path, '.gitignore'),
      ).readAsStringSync();
      expect(ignore, contains('/app/lib/brand/themes.g.dart'));
    });
  });
}
