import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../brand/themes.g.dart';
import '../../data/providers.dart';
import '../theme.dart';
import 'theme_registry.dart';
import 'theme_spec.dart';

/// Reads the themes the brand tool generated. A theme that cannot be read (the tool checks them,
/// so this is only a precaution) is left out rather than stopping the app.
List<ThemeSpec> parseBrandThemes(List<Map<String, Object>> raw) {
  final themes = <ThemeSpec>[];
  for (final entry in raw) {
    try {
      themes.add(ThemeSpec.fromJson(entry));
    } on ThemeFormatException {
      continue;
    }
  }
  return themes;
}

/// The themes built into this app: the brand folder's. Tests replace it.
final builtinThemesProvider = Provider<List<ThemeSpec>>(
  (ref) => parseBrandThemes(brandThemes),
);

/// The names of the themes the brand uses while following the system's light and dark
/// settings, or empty for the colours derived from the brand colour. Tests replace it.
final brandDefaultNamesProvider = Provider<({String light, String dark})>(
  (ref) => (light: brandDefaultLightTheme, dark: brandDefaultDarkTheme),
);

/// The themes each server offers, by server host. Filled in by the part that downloads them.
final serverThemesProvider = Provider<Map<String, List<ThemeSpec>>>(
  (ref) => const {},
);

/// Every theme the user can choose from.
final availableThemesProvider = Provider<List<AvailableTheme>>(
  (ref) => collectThemes(
    builtin: ref.watch(builtinThemesProvider),
    servers: ref.watch(serverThemesProvider),
  ),
);

/// The id of the theme the user chose, or null to follow the system.
final themeChoiceProvider = StreamProvider<String?>(
  (ref) => ref.watch(settingsRepositoryProvider).watchTheme(),
);

/// What the app is themed with.
@immutable
class AppTheme {
  const AppTheme({
    required this.light,
    required this.dark,
    required this.mode,
    this.active,
  });

  final ThemeData light;
  final ThemeData dark;
  final ThemeMode mode;

  /// The theme in use, or null while following the system with the default colours.
  final AvailableTheme? active;
}

final appThemeProvider = Provider<AppTheme>((ref) {
  final id = ref.watch(themeChoiceProvider).value;
  final choice = ThemeRef.parse(id);
  final available = ref.watch(availableThemesProvider);
  final chosen = choice == null
      ? null
      : available.where((t) => t.ref == choice).firstOrNull;

  if (chosen == null) {
    // Following the system, or the chosen theme is gone (a server stopped offering it): the
    // brand's own light and dark themes if it names them, otherwise colours from its primary.
    final names = ref.watch(brandDefaultNamesProvider);
    final builtin = ref
        .watch(availableThemesProvider)
        .where((t) => t.ref.isBuiltin);
    ThemeData forSystem(String name, Brightness brightness) {
      final match = builtin
          .where(
            (t) =>
                name.isNotEmpty &&
                t.spec.nameKey == name.trim().toLowerCase() &&
                t.spec.brightness == brightness,
          )
          .firstOrNull;
      return buildTheme(brightness, spec: match?.spec);
    }

    return AppTheme(
      light: forSystem(names.light, Brightness.light),
      dark: forSystem(names.dark, Brightness.dark),
      mode: ThemeMode.system,
    );
  }
  final theme = buildTheme(chosen.spec.brightness, spec: chosen.spec);
  return AppTheme(
    light: theme,
    dark: theme,
    mode: chosen.spec.brightness == Brightness.dark
        ? ThemeMode.dark
        : ThemeMode.light,
    active: chosen,
  );
});
