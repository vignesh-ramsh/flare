import 'package:app/app/window_class.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('window classes change at 600, 840 and 1200', () {
    const cases = [
      (0.0, WindowClass.compact),
      (390.0, WindowClass.compact),
      (599.9, WindowClass.compact),
      (600.0, WindowClass.medium),
      (839.9, WindowClass.medium),
      (840.0, WindowClass.expanded),
      (1199.9, WindowClass.expanded),
      (1200.0, WindowClass.large),
      (2560.0, WindowClass.large),
    ];
    for (final (width, expected) in cases) {
      expect(WindowClass.fromWidth(width), expected, reason: '$width');
    }
  });

  test('only expanded and large show two panes', () {
    expect(WindowClass.values.where((c) => c.hasTwoPanes), [
      WindowClass.expanded,
      WindowClass.large,
    ]);
  });
}
