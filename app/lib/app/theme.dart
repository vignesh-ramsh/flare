import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../brand/brand.g.dart';
import 'themes/theme_resolver.dart';
import 'themes/theme_spec.dart';

export 'themes/theme_spec.dart' show onColor;

/// The brand colour, for the logo and the unread badge only. Every other surface
/// uses a role from the colour scheme.
const Color brandColor = Color(Brand.primaryColor);

bool isDesktopPlatform(TargetPlatform platform) =>
    platform == TargetPlatform.windows ||
    platform == TargetPlatform.macOS ||
    platform == TargetPlatform.linux;

/// What differs between platforms, and the one colour that stays the brand's.
@immutable
class PlatformTokens extends ThemeExtension<PlatformTokens> {
  const PlatformTokens({required this.targetSize, this.accent = brandColor});

  /// The smallest comfortable tap or click target: 48 for touch, 40 with a mouse.
  final double targetSize;

  /// The colour of the unread badge and the logo's tile. The brand colour in the default
  /// theme, and the theme's own primary in any other, so it always belongs to the palette.
  final Color accent;

  /// Text or an icon on [accent].
  Color get onAccent => onColor(accent);

  @override
  PlatformTokens copyWith({double? targetSize, Color? accent}) =>
      PlatformTokens(
        targetSize: targetSize ?? this.targetSize,
        accent: accent ?? this.accent,
      );

  @override
  PlatformTokens lerp(PlatformTokens? other, double t) => this;

  // Equal tokens make equal themes, so rebuilding the same theme does not animate "to" itself.
  @override
  bool operator ==(Object other) =>
      other is PlatformTokens &&
      other.targetSize == targetSize &&
      other.accent == accent;

  @override
  int get hashCode => Object.hash(targetSize, accent);
}

extension ThemeTokens on BuildContext {
  PlatformTokens get tokens => Theme.of(this).extension<PlatformTokens>()!;
}

/// The colours of the default theme: Material 3 from the brand colour.
ResolvedTheme defaultResolvedTheme(Brightness brightness) {
  final seeded = ColorScheme.fromSeed(
    seedColor: brandColor,
    brightness: brightness,
  );
  return ResolvedTheme(scheme: seeded, background: seeded.surface);
}

/// Material 3 from the brand colour, or from [spec] when the user chose a theme. The same
/// widgets run on every platform; what differs is the target size here and the typeface Flutter
/// picks per platform.
ThemeData buildTheme(
  Brightness brightness, {
  TargetPlatform? platform,
  ThemeSpec? spec,
}) {
  final target = platform ?? defaultTargetPlatform;
  final desktop = isDesktopPlatform(target);
  final ResolvedTheme resolved;
  final Color accent;
  if (spec != null) {
    resolved = resolveTheme(spec);
    accent = resolved.scheme.primary;
  } else {
    resolved = defaultResolvedTheme(brightness);
    accent = brandColor;
  }
  final scheme = resolved.scheme;

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: resolved.background,
    platform: target,
    visualDensity: desktop ? VisualDensity.compact : VisualDensity.standard,
    materialTapTargetSize: desktop
        ? MaterialTapTargetSize.shrinkWrap
        : MaterialTapTargetSize.padded,
    extensions: [PlatformTokens(targetSize: desktop ? 40 : 48, accent: accent)],
    // Spacing is a 4 dp grid; corners are 8 on fields and chips, 12 on cards,
    // 16 on sheets and 28 on dialogs.
    cardTheme: CardThemeData(
      elevation: 0,
      margin: EdgeInsets.zero,
      color: scheme.surfaceContainerLow,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: scheme.outlineVariant),
      ),
    ),
    chipTheme: ChipThemeData(
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    ),
    inputDecorationTheme: InputDecorationTheme(
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
    ),
    bottomSheetTheme: const BottomSheetThemeData(
      showDragHandle: true,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
    ),
    dialogTheme: DialogThemeData(
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(28)),
    ),
    dividerTheme: DividerThemeData(
      color: scheme.outlineVariant,
      space: 1,
      thickness: 1,
    ),
  );
}
