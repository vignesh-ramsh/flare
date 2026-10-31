import 'package:app/app/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

double contrast(Color a, Color b) {
  final hi = a.computeLuminance() > b.computeLuminance() ? a : b;
  final lo = identical(hi, a) ? b : a;
  return (hi.computeLuminance() + 0.05) / (lo.computeLuminance() + 0.05);
}

void main() {
  test('text on the brand colour reads at 4.5 to 1', () {
    expect(
      contrast(brandColor, onColor(brandColor)),
      greaterThanOrEqualTo(4.5),
    );
  });

  test('onColor picks the better of black and white', () {
    expect(onColor(Colors.white), Colors.black);
    expect(onColor(Colors.black), Colors.white);
    expect(onColor(const Color(0xFF0F172A)), Colors.white);
    expect(onColor(const Color(0xFFFACC15)), Colors.black);
  });

  test('light and dark themes use their own brightness', () {
    expect(
      buildTheme(Brightness.light).colorScheme.brightness,
      Brightness.light,
    );
    expect(buildTheme(Brightness.dark).colorScheme.brightness, Brightness.dark);
  });

  test('targets are 40 dp with a mouse and 48 dp for touch', () {
    for (final p in [
      TargetPlatform.windows,
      TargetPlatform.macOS,
      TargetPlatform.linux,
    ]) {
      expect(
        buildTheme(
          Brightness.light,
          platform: p,
        ).extension<PlatformTokens>()!.targetSize,
        40,
        reason: '$p',
      );
    }
    for (final p in [TargetPlatform.android, TargetPlatform.iOS]) {
      expect(
        buildTheme(
          Brightness.light,
          platform: p,
        ).extension<PlatformTokens>()!.targetSize,
        48,
        reason: '$p',
      );
    }
  });

  test('corners follow the design: 8 chips, 12 cards, 28 dialogs', () {
    final theme = buildTheme(Brightness.light);
    RoundedRectangleBorder shape(ShapeBorder? s) =>
        s! as RoundedRectangleBorder;
    expect(
      shape(theme.cardTheme.shape).borderRadius,
      BorderRadius.circular(12),
    );
    expect(shape(theme.chipTheme.shape).borderRadius, BorderRadius.circular(8));
    expect(
      shape(theme.dialogTheme.shape).borderRadius,
      BorderRadius.circular(28),
    );
  });
}
