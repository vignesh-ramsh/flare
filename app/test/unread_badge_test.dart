import 'package:app/app/theme.dart';
import 'package:app/ui/unread_badge.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'theme_test.dart' show contrast;

void main() {
  Future<void> pumpBadge(WidgetTester tester, int count) => tester.pumpWidget(
    MaterialApp(
      home: Scaffold(body: UnreadBadge(count: count)),
    ),
  );

  testWidgets(
    'draws the count in the brand colour, with text that reads on it',
    (tester) async {
      await pumpBadge(tester, 3);

      final box = tester.widget<DecoratedBox>(
        find.descendant(
          of: find.byType(UnreadBadge),
          matching: find.byType(DecoratedBox),
        ),
      );
      expect((box.decoration as BoxDecoration).color, brandColor);

      final text = tester.widget<Text>(find.text('3'));
      expect(text.style!.color, onColor(brandColor));
      expect(
        contrast(brandColor, text.style!.color!),
        greaterThanOrEqualTo(4.5),
      );
    },
  );

  testWidgets('caps the count at 99+', (tester) async {
    await pumpBadge(tester, 100);
    expect(find.text('99+'), findsOneWidget);
    await pumpBadge(tester, 99);
    expect(find.text('99'), findsOneWidget);
  });

  testWidgets('draws nothing for zero', (tester) async {
    await pumpBadge(tester, 0);
    expect(find.byType(DecoratedBox), findsNothing);
    expect(find.byType(Text), findsNothing);
  });
}
