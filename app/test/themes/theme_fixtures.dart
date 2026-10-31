import 'package:app/app/themes/theme_spec.dart';
import 'package:flutter/material.dart';

/// A theme that sets every colour, each a different value so a mix-up shows.
ThemeSpec fullTheme({String name = 'Full'}) {
  const colors = <ThemeToken, int>{
    ThemeToken.primary: 0x1B5E20,
    ThemeToken.onPrimary: 0xFFFFFF,
    ThemeToken.primaryContainer: 0xC8E6C9,
    ThemeToken.onPrimaryContainer: 0x0B2E0E,
    ThemeToken.secondary: 0x37474F,
    ThemeToken.onSecondary: 0xFFFFFF,
    ThemeToken.secondaryContainer: 0xCFD8DC,
    ThemeToken.onSecondaryContainer: 0x1C262B,
    ThemeToken.tertiary: 0x6A1B9A,
    ThemeToken.onTertiary: 0xFFFFFF,
    ThemeToken.tertiaryContainer: 0xE1BEE7,
    ThemeToken.onTertiaryContainer: 0x2B0A3D,
    ThemeToken.error: 0xB71C1C,
    ThemeToken.onError: 0xFFFFFF,
    ThemeToken.errorContainer: 0xFFCDD2,
    ThemeToken.onErrorContainer: 0x410002,
    ThemeToken.background: 0xF1F8E9,
    ThemeToken.surface: 0xFAFFF5,
    ThemeToken.surfaceContainerLowest: 0xFFFFFF,
    ThemeToken.surfaceContainerLow: 0xF4FAEE,
    ThemeToken.surfaceContainer: 0xEDF4E7,
    ThemeToken.surfaceContainerHigh: 0xE6EDE0,
    ThemeToken.surfaceContainerHighest: 0xDFE6D9,
    ThemeToken.onSurface: 0x1A1C19,
    ThemeToken.onSurfaceVariant: 0x42493F,
    ThemeToken.border: 0x72796E,
    ThemeToken.borderSubtle: 0xC2C9BB,
    ThemeToken.shadow: 0x000000,
    ThemeToken.scrim: 0x000000,
    ThemeToken.inverseSurface: 0x2F312D,
    ThemeToken.onInverseSurface: 0xF1F1EB,
    ThemeToken.inversePrimary: 0x8BD78F,
  };
  return ThemeSpec(
    name: name,
    brightness: Brightness.light,
    colors: {
      for (final e in colors.entries) e.key: Color(0xFF000000 | e.value),
    },
  );
}

/// A dark theme that sets only a few colours; the rest are derived.
ThemeSpec midnight() => ThemeSpec(
  name: 'Midnight',
  brightness: Brightness.dark,
  colors: const {
    ThemeToken.primary: Color(0xFF8AB4F8),
    ThemeToken.background: Color(0xFF0B1220),
    ThemeToken.surface: Color(0xFF111A2B),
    ThemeToken.onSurface: Color(0xFFE6EDF7),
  },
);

/// A light theme made of one colour.
ThemeSpec paper() => ThemeSpec.seeded(
  name: 'Paper',
  brightness: Brightness.light,
  primary: const Color(0xFF455A64),
);

/// A theme whose text cannot be read on its page.
ThemeSpec unreadable() => ThemeSpec(
  name: 'Murky',
  brightness: Brightness.light,
  colors: const {
    ThemeToken.primary: Color(0xFF455A64),
    ThemeToken.surface: Color(0xFF808080),
    ThemeToken.onSurface: Color(0xFF858585),
  },
);
