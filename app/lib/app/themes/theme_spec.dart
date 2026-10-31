import 'package:flutter/material.dart';

/// Every colour a theme can set. [key] is the name used in theme files and the server's API.
///
/// A theme need not set them all: what it leaves out is derived from its `primary`, so the
/// smallest theme is a name, light or dark, and one colour. Built-in themes list them all.
enum ThemeToken {
  // The main colour and what sits on it.
  primary('primary'),
  onPrimary('on_primary'),
  primaryContainer('primary_container'),
  onPrimaryContainer('on_primary_container'),
  secondary('secondary'),
  onSecondary('on_secondary'),
  secondaryContainer('secondary_container'),
  onSecondaryContainer('on_secondary_container'),
  tertiary('tertiary'),
  onTertiary('on_tertiary'),
  tertiaryContainer('tertiary_container'),
  onTertiaryContainer('on_tertiary_container'),
  // Errors and urgent messages.
  error('error'),
  onError('on_error'),
  errorContainer('error_container'),
  onErrorContainer('on_error_container'),
  // The page behind everything, and the layers on it, lowest to highest.
  background('background'),
  surface('surface'),
  surfaceContainerLowest('surface_container_lowest'),
  surfaceContainerLow('surface_container_low'),
  surfaceContainer('surface_container'),
  surfaceContainerHigh('surface_container_high'),
  surfaceContainerHighest('surface_container_highest'),
  // Text and icons on a surface.
  onSurface('on_surface'),
  onSurfaceVariant('on_surface_variant'),
  // Lines.
  border('border'),
  borderSubtle('border_subtle'),
  // Effects, and the inverted snackbar.
  shadow('shadow'),
  scrim('scrim'),
  inverseSurface('inverse_surface'),
  onInverseSurface('on_inverse_surface'),
  inversePrimary('inverse_primary');

  const ThemeToken(this.key);

  final String key;

  static final Map<String, ThemeToken> _byKey = {
    for (final token in values) token.key: token,
  };

  static ThemeToken? fromKey(String key) => _byKey[key];
}

/// The text that must stay readable on what it sits on, as `(text, background)`. Below
/// [minimumContrast] a theme is refused: a theme nobody can read could not be switched back.
const List<(ThemeToken, ThemeToken)> readablePairs = [
  (ThemeToken.onSurface, ThemeToken.surface),
  (ThemeToken.onSurface, ThemeToken.background),
  (ThemeToken.onSurfaceVariant, ThemeToken.surface),
  (ThemeToken.onPrimary, ThemeToken.primary),
  (ThemeToken.onPrimaryContainer, ThemeToken.primaryContainer),
  (ThemeToken.onSecondary, ThemeToken.secondary),
  (ThemeToken.onSecondaryContainer, ThemeToken.secondaryContainer),
  (ThemeToken.onTertiary, ThemeToken.tertiary),
  (ThemeToken.onTertiaryContainer, ThemeToken.tertiaryContainer),
  (ThemeToken.onError, ThemeToken.error),
  (ThemeToken.onErrorContainer, ThemeToken.errorContainer),
  (ThemeToken.onInverseSurface, ThemeToken.inverseSurface),
];

/// The least contrast (WCAG ratio) a theme may have between text and its background.
const double minimumContrast = 3.0;

/// What WCAG asks for ordinary text; the Admin UI warns below it.
const double recommendedContrast = 4.5;

/// The WCAG contrast ratio of two colours, 1 to 21.
double contrastRatio(Color a, Color b) {
  final la = a.computeLuminance();
  final lb = b.computeLuminance();
  final lighter = la > lb ? la : lb;
  final darker = la > lb ? lb : la;
  return (lighter + 0.05) / (darker + 0.05);
}

/// Black or white, whichever reads better on [background].
Color onColor(Color background) {
  final withBlack = contrastRatio(background, Colors.black);
  final withWhite = contrastRatio(background, Colors.white);
  return withBlack >= withWhite ? Colors.black : Colors.white;
}

/// A theme file or server answer that is not a theme.
class ThemeFormatException implements Exception {
  const ThemeFormatException(this.message);

  final String message;

  @override
  String toString() => 'ThemeFormatException: $message';
}

/// `#RRGGBB` as a colour, or null.
Color? parseHexColor(String text) {
  final match = RegExp(r'^#([0-9A-Fa-f]{6})$').firstMatch(text.trim());
  if (match == null) return null;
  return Color(0xFF000000 | int.parse(match.group(1)!, radix: 16));
}

/// A colour as `#RRGGBB`, upper case.
String formatHexColor(Color color) =>
    '#${(color.toARGB32() & 0xFFFFFF).toRadixString(16).padLeft(6, '0').toUpperCase()}';

/// A theme: a unique [name], whether it is [brightness] light or dark, and the colours it sets.
@immutable
class ThemeSpec {
  const ThemeSpec({
    required this.name,
    required this.brightness,
    required this.colors,
  });

  /// A theme made of one colour: every other colour is derived from it.
  ThemeSpec.seeded({
    required this.name,
    required this.brightness,
    required Color primary,
  }) : colors = {ThemeToken.primary: primary};

  /// The name: letters, digits, spaces, dots, dashes and underscores, 1 to 32 characters. Names
  /// are compared without regard to case, so "Midnight" and "midnight" are the same theme.
  final String name;
  final Brightness brightness;

  /// Only the colours the theme sets.
  final Map<ThemeToken, Color> colors;

  static final RegExp namePattern = RegExp(
    r'^[A-Za-z0-9][A-Za-z0-9 _.-]{0,31}$',
  );

  static bool isValidName(String name) => namePattern.hasMatch(name);

  /// The name compared by: trimmed and lower case.
  String get nameKey => name.trim().toLowerCase();

  /// Reads a theme as the brand tool and the server write it:
  ///
  ///     {"name": "Midnight", "brightness": "dark", "colors": {"primary": "#8AB4F8", ...}}
  ///
  /// Colours this version does not know are ignored, so a newer server's theme still opens.
  /// Throws [ThemeFormatException] for anything else that is wrong.
  factory ThemeSpec.fromJson(Object? json) {
    if (json is! Map) {
      throw const ThemeFormatException('a theme must be an object');
    }
    final name = json['name'];
    if (name is! String || !isValidName(name)) {
      throw ThemeFormatException(
        'the name must be 1 to 32 letters, digits, spaces, dots, dashes or underscores (found $name)',
      );
    }
    final brightness = switch (json['brightness']) {
      'light' => Brightness.light,
      'dark' => Brightness.dark,
      final other => throw ThemeFormatException(
        '"$name": brightness must be light or dark (found $other)',
      ),
    };
    final raw = json['colors'];
    if (raw is! Map) {
      throw ThemeFormatException('"$name": colors must be an object');
    }
    final colors = <ThemeToken, Color>{};
    for (final entry in raw.entries) {
      final token = ThemeToken.fromKey('${entry.key}');
      if (token == null) continue;
      final value = entry.value;
      final color = value is String ? parseHexColor(value) : null;
      if (color == null) {
        throw ThemeFormatException(
          '"$name": ${token.key} must be a colour like #RRGGBB (found $value)',
        );
      }
      colors[token] = color;
    }
    if (!colors.containsKey(ThemeToken.primary)) {
      throw ThemeFormatException('"$name": primary is required');
    }
    return ThemeSpec(name: name, brightness: brightness, colors: colors);
  }

  Map<String, Object> toJson() => {
    'name': name,
    'brightness': brightness == Brightness.dark ? 'dark' : 'light',
    'colors': {
      for (final token in ThemeToken.values)
        if (colors[token] case final color?) token.key: formatHexColor(color),
    },
  };

  @override
  bool operator ==(Object other) =>
      other is ThemeSpec &&
      other.name == name &&
      other.brightness == brightness &&
      _sameColors(other.colors);

  bool _sameColors(Map<ThemeToken, Color> other) {
    if (other.length != colors.length) return false;
    for (final entry in colors.entries) {
      if (other[entry.key] != entry.value) return false;
    }
    return true;
  }

  @override
  int get hashCode => Object.hash(
    name,
    brightness,
    Object.hashAllUnordered(
      colors.entries.map((e) => Object.hash(e.key, e.value)),
    ),
  );

  @override
  String toString() =>
      'ThemeSpec($name, ${brightness.name}, ${colors.length} colours)';
}
