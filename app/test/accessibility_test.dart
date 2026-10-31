import 'package:flutter/material.dart' show Brightness;
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';

/// Tap targets, labels and contrast on the screens a user sees most, at the
/// widths where the layout differs.
void main() {
  for (final width in [390.0, 1400.0]) {
    for (final location in [
      '/',
      '/topic/example.com/alerts-db',
      '/inbox',
      '/settings',
    ]) {
      testApp(
        '${width.toInt()} dp $location meets the accessibility guidelines',
        (tester, db) async {
          final handle = tester.ensureSemantics();
          await pumpApp(tester, db, width: width, location: location);

          await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
          await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
          await expectLater(tester, meetsGuideline(textContrastGuideline));
          handle.dispose();
        },
      );
    }
  }

  testApp('dark theme meets the contrast guideline', (tester, db) async {
    final handle = tester.ensureSemantics();
    await pumpApp(tester, db, width: 390, brightness: Brightness.dark);
    await expectLater(tester, meetsGuideline(textContrastGuideline));
    handle.dispose();
  });
}
