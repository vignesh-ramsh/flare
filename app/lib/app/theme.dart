import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../brand/brand.g.dart';

/// The brand colour, for the logo and the unread badge only. Every other surface
/// uses a role from the colour scheme.
const Color brandColor = Color(Brand.primaryColor);

/// Black or white, whichever reads better on [background].
Color onColor(Color background) {
  final withBlack = _contrast(background, Colors.black);
  final withWhite = _contrast(background, Colors.white);
  return withBlack >= withWhite ? Colors.black : Colors.white;
}

double _contrast(Color a, Color b) {
  final la = a.computeLuminance();
  final lb = b.computeLuminance();
  final lighter = la > lb ? la : lb;
  final darker = la > lb ? lb : la;
  return (lighter + 0.05) / (darker + 0.05);
}

bool isDesktopPlatform(TargetPlatform platform) =>
    platform == TargetPlatform.windows ||
    platform == TargetPlatform.macOS ||
    platform == TargetPlatform.linux;

/// Sizes that differ between touch and pointer platforms.
@immutable
class PlatformTokens extends ThemeExtension<PlatformTokens> {
  const PlatformTokens({required this.targetSize});

  /// The smallest comfortable tap or click target: 48 for touch, 40 with a mouse.
  final double targetSize;

  @override
  PlatformTokens copyWith({double? targetSize}) =>
      PlatformTokens(targetSize: targetSize ?? this.targetSize);

  @override
  PlatformTokens lerp(PlatformTokens? other, double t) => this;
}

extension ThemeTokens on BuildContext {
  PlatformTokens get tokens => Theme.of(this).extension<PlatformTokens>()!;
}

/// Material 3 from the brand colour. The same widgets run on every platform; what
/// differs is the target size here and the typeface Flutter picks per platform.
ThemeData buildTheme(Brightness brightness, {TargetPlatform? platform}) {
  final target = platform ?? defaultTargetPlatform;
  final desktop = isDesktopPlatform(target);
  final scheme = ColorScheme.fromSeed(
    seedColor: brandColor,
    brightness: brightness,
  );

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    platform: target,
    visualDensity: desktop ? VisualDensity.compact : VisualDensity.standard,
    materialTapTargetSize: desktop
        ? MaterialTapTargetSize.shrinkWrap
        : MaterialTapTargetSize.padded,
    extensions: [PlatformTokens(targetSize: desktop ? 40 : 48)],
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
