import 'dart:math' as math;

import 'package:yaml/yaml.dart';

import 'spec.dart';

/// The colours a theme can set, in the order they are written. The app, the server and the Admin
/// UI keep the same list; a test in each checks it against `api/theme-tokens.json`.
const themeTokenKeys = [
  'primary',
  'on_primary',
  'primary_container',
  'on_primary_container',
  'secondary',
  'on_secondary',
  'secondary_container',
  'on_secondary_container',
  'tertiary',
  'on_tertiary',
  'tertiary_container',
  'on_tertiary_container',
  'error',
  'on_error',
  'error_container',
  'on_error_container',
  'background',
  'surface',
  'surface_container_lowest',
  'surface_container_low',
  'surface_container',
  'surface_container_high',
  'surface_container_highest',
  'on_surface',
  'on_surface_variant',
  'border',
  'border_subtle',
  'shadow',
  'scrim',
  'inverse_surface',
  'on_inverse_surface',
  'inverse_primary',
];

/// Text that must stay readable on what it sits on, as `(text, background)`.
const themeReadablePairs = [
  ('on_surface', 'surface'),
  ('on_surface', 'background'),
  ('on_surface_variant', 'surface'),
  ('on_primary', 'primary'),
  ('on_primary_container', 'primary_container'),
  ('on_secondary', 'secondary'),
  ('on_secondary_container', 'secondary_container'),
  ('on_tertiary', 'tertiary'),
  ('on_tertiary_container', 'tertiary_container'),
  ('on_error', 'error'),
  ('on_error_container', 'error_container'),
  ('on_inverse_surface', 'inverse_surface'),
];

/// The least contrast between a text colour and its background that a theme may set.
const themeMinimumContrast = 3.0;

/// Most themes a brand may list; each becomes a choice in Settings.
const maxBrandThemes = 50;

final _themeName = RegExp(r'^[A-Za-z0-9][A-Za-z0-9 _.-]{0,31}$');
final _hex = RegExp(r'^#[0-9a-fA-F]{6}$');

/// One theme, validated: its colours as `#RRGGBB` in upper case, in the order of
/// [themeTokenKeys].
final class BrandTheme {
  BrandTheme({
    required this.name,
    required this.brightness,
    required this.colors,
  });

  final String name;

  /// `light` or `dark`.
  final String brightness;
  final Map<String, String> colors;
}

/// The validated contents of a brand's themes file.
final class BrandThemes {
  const BrandThemes({
    this.themes = const [],
    this.defaultLight = '',
    this.defaultDark = '',
  });

  final List<BrandTheme> themes;

  /// The theme used while following the system's light setting, or empty to use the colours
  /// derived from the brand colour. Likewise [defaultDark].
  final String defaultLight;
  final String defaultDark;
}

double _luminance(String hex) {
  final v = int.parse(hex.substring(1), radix: 16);
  double channel(int c) {
    final s = c / 255;
    return s <= 0.03928
        ? s / 12.92
        : math.pow((s + 0.055) / 1.055, 2.4).toDouble();
  }

  return 0.2126 * channel((v >> 16) & 0xFF) +
      0.7152 * channel((v >> 8) & 0xFF) +
      0.0722 * channel(v & 0xFF);
}

/// The WCAG contrast ratio of two `#RRGGBB` colours.
double contrastOf(String a, String b) {
  final la = _luminance(a);
  final lb = _luminance(b);
  final hi = math.max(la, lb);
  final lo = math.min(la, lb);
  return (hi + 0.05) / (lo + 0.05);
}

/// Reads and validates a themes file. Problems go to [report], each naming the theme and the
/// colour; the result is null if there were any errors.
BrandThemes? parseThemesYaml(
  String text,
  Report report, {
  String where = 'themes.yaml',
}) {
  Object? root;
  try {
    root = loadYaml(text);
  } on YamlException catch (e) {
    report.error(where, 'is not valid YAML: ${e.message}');
    return null;
  }
  if (root == null) return const BrandThemes();
  if (root is! Map) {
    report.error(where, 'must contain a mapping with a "themes" list');
    return null;
  }
  final before = report.errors.length;

  const known = {'schema', 'defaults', 'themes'};
  for (final key in root.keys) {
    if (!known.contains('$key')) {
      report.error(
        where,
        'unknown setting "$key" (expected schema, defaults or themes)',
      );
    }
  }
  final schema = root['schema'];
  if (schema != 1) {
    report.error('$where schema', 'must be 1 (found $schema)');
  }

  final list = root['themes'];
  final themes = <BrandTheme>[];
  if (list == null) {
    // no themes: only the defaults could be wrong
  } else if (list is! List) {
    report.error('$where themes', 'must be a list of themes');
  } else {
    if (list.length > maxBrandThemes) {
      report.error(
        '$where themes',
        'has ${list.length} themes; at most $maxBrandThemes',
      );
    }
    final seen = <String, String>{};
    for (var i = 0; i < list.length; i++) {
      final theme = _parseTheme(list[i], i, where, report);
      if (theme == null) continue;
      final earlier = seen[theme.name.toLowerCase()];
      if (earlier != null) {
        report.error(
          '$where themes[$i]',
          'the name "${theme.name}" is already used by "$earlier"; names must be unique, whatever their case',
        );
        continue;
      }
      seen[theme.name.toLowerCase()] = theme.name;
      themes.add(theme);
    }
  }

  var defaultLight = '';
  var defaultDark = '';
  final defaults = root['defaults'];
  if (defaults != null) {
    if (defaults is! Map) {
      report.error('$where defaults', 'must have light and dark');
    } else {
      for (final key in defaults.keys) {
        if ('$key' != 'light' && '$key' != 'dark') {
          report.error(
            '$where defaults',
            'unknown setting "$key" (expected light or dark)',
          );
        }
      }
      String pick(String which) {
        final value = defaults[which];
        if (value == null || '$value'.trim().isEmpty) return '';
        final name = '$value'.trim();
        final match = themes.where(
          (t) => t.name.toLowerCase() == name.toLowerCase(),
        );
        if (match.isEmpty) {
          report.error(
            '$where defaults.$which',
            'names the theme "$name", which is not in the list of themes',
          );
          return '';
        }
        if (match.first.brightness != which) {
          report.error(
            '$where defaults.$which',
            '"${match.first.name}" is a ${match.first.brightness} theme; the $which default must be a $which one',
          );
          return '';
        }
        return match.first.name;
      }

      defaultLight = pick('light');
      defaultDark = pick('dark');
    }
  }

  if (report.errors.length > before) return null;
  return BrandThemes(
    themes: themes,
    defaultLight: defaultLight,
    defaultDark: defaultDark,
  );
}

BrandTheme? _parseTheme(Object? raw, int index, String file, Report report) {
  var where = '$file themes[$index]';
  if (raw is! Map) {
    report.error(where, 'must be a mapping with name, brightness and colors');
    return null;
  }
  final before = report.errors.length;
  final name = raw['name'];
  if (name is! String || !_themeName.hasMatch(name)) {
    report.error(
      where,
      'name must be 1 to 32 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit (found $name)',
    );
  } else {
    where = '$file theme "$name"';
  }
  final brightness = raw['brightness'];
  if (brightness != 'light' && brightness != 'dark') {
    report.error(where, 'brightness must be light or dark (found $brightness)');
  }
  for (final key in raw.keys) {
    if (!{'name', 'brightness', 'colors'}.contains('$key')) {
      report.error(
        where,
        'unknown setting "$key" (expected name, brightness or colors)',
      );
    }
  }

  final colorsRaw = raw['colors'];
  final colors = <String, String>{};
  if (colorsRaw is! Map) {
    report.error(where, 'colors must list the colours the theme sets');
  } else {
    for (final entry in colorsRaw.entries) {
      final key = '${entry.key}';
      if (!themeTokenKeys.contains(key)) {
        report.error(
          '$where colors',
          '"$key" is not a colour a theme can set (see api/theme-tokens.json for the list)',
        );
        continue;
      }
      final value = entry.value;
      if (value is! String || !_hex.hasMatch(value.trim())) {
        report.error(
          '$where colors.$key',
          'must be a colour like "#RRGGBB", in quotes (found $value)',
        );
        continue;
      }
      colors[key] = value.trim().toUpperCase();
    }
    if (!colors.containsKey('primary') &&
        !report.errors.any((e) => e.startsWith('$where colors.primary'))) {
      report.error(
        where,
        'colors.primary is required: every colour left out is derived from it',
      );
    }
  }

  if (report.errors.length > before) return null;

  // Text and background both set by the theme: they must be readable together. A colour the
  // theme leaves out is derived by the app, which checks the result when it loads the theme.
  for (final (text, behind) in themeReadablePairs) {
    final a = colors[text];
    final b = colors[behind];
    if (a == null || b == null) continue;
    final ratio = contrastOf(a, b);
    if (ratio < themeMinimumContrast) {
      report.error(
        '$where colors',
        '$text ($a) on $behind ($b) has a contrast of ${ratio.toStringAsFixed(2)}:1; '
            'it must be at least ${themeMinimumContrast.toStringAsFixed(1)}:1 to be readable',
      );
    }
  }
  if (report.errors.length > before) return null;

  if (colors.containsKey('background') && !colors.containsKey('surface')) {
    report.warn(
      where,
      'sets background but not surface; the surface is derived from the primary colour and may not match',
    );
  }
  return BrandTheme(
    name: name as String,
    brightness: brightness as String,
    colors: {
      for (final key in themeTokenKeys)
        if (colors.containsKey(key)) key: colors[key]!,
    },
  );
}
