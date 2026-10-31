import 'package:flutter/material.dart';

import 'theme_spec.dart';

/// A theme with every colour decided: the ones it set, and the rest derived from its primary.
@immutable
class ResolvedTheme {
  const ResolvedTheme({required this.scheme, required this.background});

  final ColorScheme scheme;

  /// The page behind everything (`ThemeData.scaffoldBackgroundColor`).
  final Color background;

  /// The colour a token has in this theme, set or derived.
  Color operator [](ThemeToken token) => switch (token) {
    ThemeToken.primary => scheme.primary,
    ThemeToken.onPrimary => scheme.onPrimary,
    ThemeToken.primaryContainer => scheme.primaryContainer,
    ThemeToken.onPrimaryContainer => scheme.onPrimaryContainer,
    ThemeToken.secondary => scheme.secondary,
    ThemeToken.onSecondary => scheme.onSecondary,
    ThemeToken.secondaryContainer => scheme.secondaryContainer,
    ThemeToken.onSecondaryContainer => scheme.onSecondaryContainer,
    ThemeToken.tertiary => scheme.tertiary,
    ThemeToken.onTertiary => scheme.onTertiary,
    ThemeToken.tertiaryContainer => scheme.tertiaryContainer,
    ThemeToken.onTertiaryContainer => scheme.onTertiaryContainer,
    ThemeToken.error => scheme.error,
    ThemeToken.onError => scheme.onError,
    ThemeToken.errorContainer => scheme.errorContainer,
    ThemeToken.onErrorContainer => scheme.onErrorContainer,
    ThemeToken.background => background,
    ThemeToken.surface => scheme.surface,
    ThemeToken.surfaceContainerLowest => scheme.surfaceContainerLowest,
    ThemeToken.surfaceContainerLow => scheme.surfaceContainerLow,
    ThemeToken.surfaceContainer => scheme.surfaceContainer,
    ThemeToken.surfaceContainerHigh => scheme.surfaceContainerHigh,
    ThemeToken.surfaceContainerHighest => scheme.surfaceContainerHighest,
    ThemeToken.onSurface => scheme.onSurface,
    ThemeToken.onSurfaceVariant => scheme.onSurfaceVariant,
    ThemeToken.border => scheme.outline,
    ThemeToken.borderSubtle => scheme.outlineVariant,
    ThemeToken.shadow => scheme.shadow,
    ThemeToken.scrim => scheme.scrim,
    ThemeToken.inverseSurface => scheme.inverseSurface,
    ThemeToken.onInverseSurface => scheme.onInverseSurface,
    ThemeToken.inversePrimary => scheme.inversePrimary,
  };

  /// The pairs of text and background that fall short of [minimum] contrast.
  List<ReadabilityProblem> readabilityProblems({
    double minimum = minimumContrast,
  }) {
    return [
      for (final (text, behind) in readablePairs)
        if (contrastRatio(this[text], this[behind]) case final ratio
            when ratio < minimum)
          ReadabilityProblem(text: text, background: behind, ratio: ratio),
    ];
  }
}

/// Text that is hard to read on its background in a theme.
@immutable
class ReadabilityProblem {
  const ReadabilityProblem({
    required this.text,
    required this.background,
    required this.ratio,
  });

  final ThemeToken text;
  final ThemeToken background;
  final double ratio;

  @override
  String toString() =>
      '${text.key} on ${background.key} is ${ratio.toStringAsFixed(2)}:1';
}

/// Decides every colour of [spec]: those it sets are used exactly, the rest are derived.
///
/// What a theme leaves out comes from Material 3's palette for its `primary`, in its brightness,
/// except text on a colour the theme did set: that is black or white, whichever reads better
/// on the colour the theme actually chose (a yellow primary with Material's white text would
/// be unreadable), and the layers of surface are steps from the surface the theme chose.
ResolvedTheme resolveTheme(ThemeSpec spec) {
  Color? c(ThemeToken token) => spec.colors[token];
  final primary = spec.colors[ThemeToken.primary]!;
  final dark = spec.brightness == Brightness.dark;

  /// [text] if set, otherwise readable text on [base] if that was set.
  Color? textOn(ThemeToken text, ThemeToken base) {
    final given = c(text);
    if (given != null) return given;
    final under = c(base);
    return under == null ? null : onColor(under);
  }

  final surface = c(ThemeToken.surface);
  final onSurface = textOn(ThemeToken.onSurface, ThemeToken.surface);

  /// A step from the surface towards the text colour, for the layers of surface.
  Color? layer(ThemeToken token, double amount) {
    final given = c(token);
    if (given != null) return given;
    if (surface == null) return null;
    final toward = onSurface ?? (dark ? Colors.white : Colors.black);
    return Color.alphaBlend(toward.withValues(alpha: amount), surface);
  }

  final lowest =
      c(ThemeToken.surfaceContainerLowest) ??
      (surface == null
          ? null
          : Color.alphaBlend(
              (dark ? Colors.black : Colors.white).withValues(alpha: 0.6),
              surface,
            ));

  final scheme = ColorScheme.fromSeed(
    seedColor: primary,
    brightness: spec.brightness,
    primary: primary,
    onPrimary: textOn(ThemeToken.onPrimary, ThemeToken.primary),
    primaryContainer: c(ThemeToken.primaryContainer),
    onPrimaryContainer: textOn(
      ThemeToken.onPrimaryContainer,
      ThemeToken.primaryContainer,
    ),
    secondary: c(ThemeToken.secondary),
    onSecondary: textOn(ThemeToken.onSecondary, ThemeToken.secondary),
    secondaryContainer: c(ThemeToken.secondaryContainer),
    onSecondaryContainer: textOn(
      ThemeToken.onSecondaryContainer,
      ThemeToken.secondaryContainer,
    ),
    tertiary: c(ThemeToken.tertiary),
    onTertiary: textOn(ThemeToken.onTertiary, ThemeToken.tertiary),
    tertiaryContainer: c(ThemeToken.tertiaryContainer),
    onTertiaryContainer: textOn(
      ThemeToken.onTertiaryContainer,
      ThemeToken.tertiaryContainer,
    ),
    error: c(ThemeToken.error),
    onError: textOn(ThemeToken.onError, ThemeToken.error),
    errorContainer: c(ThemeToken.errorContainer),
    onErrorContainer: textOn(
      ThemeToken.onErrorContainer,
      ThemeToken.errorContainer,
    ),
    surface: surface,
    onSurface: onSurface,
    onSurfaceVariant:
        c(ThemeToken.onSurfaceVariant) ??
        (surface != null && onSurface != null
            ? Color.alphaBlend(onSurface.withValues(alpha: 0.75), surface)
            : null),
    surfaceContainerLowest: lowest,
    surfaceContainerLow: layer(ThemeToken.surfaceContainerLow, 0.03),
    surfaceContainer: layer(ThemeToken.surfaceContainer, 0.05),
    surfaceContainerHigh: layer(ThemeToken.surfaceContainerHigh, 0.08),
    surfaceContainerHighest: layer(ThemeToken.surfaceContainerHighest, 0.11),
    outline: c(ThemeToken.border),
    outlineVariant: c(ThemeToken.borderSubtle),
    shadow: c(ThemeToken.shadow),
    scrim: c(ThemeToken.scrim),
    inverseSurface: c(ThemeToken.inverseSurface),
    onInverseSurface: textOn(
      ThemeToken.onInverseSurface,
      ThemeToken.inverseSurface,
    ),
    inversePrimary: c(ThemeToken.inversePrimary),
  );
  return ResolvedTheme(
    scheme: scheme,
    background: c(ThemeToken.background) ?? scheme.surface,
  );
}
