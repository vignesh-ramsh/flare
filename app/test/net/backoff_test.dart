import 'dart:math';

import 'package:app/net/backoff.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'doubles up to the cap, with the delay between half and all of each step',
    () {
      final backoff = Backoff(random: Random(1));
      const ceilings = [1, 2, 4, 8, 16, 32, 60, 60, 60];
      for (final ceiling in ceilings) {
        final delay = backoff.next();
        expect(
          delay.inMicroseconds,
          lessThanOrEqualTo(ceiling * 1000000),
          reason: '$ceiling',
        );
        expect(
          delay.inMicroseconds,
          greaterThanOrEqualTo(ceiling * 500000),
          reason: '$ceiling',
        );
      }
    },
  );

  test('counts failures and starts over after a reset', () {
    final backoff = Backoff(random: Random(1));
    backoff.next();
    backoff.next();
    expect(backoff.failures, 2);

    backoff.reset();
    expect(backoff.failures, 0);
    expect(backoff.next().inMicroseconds, lessThanOrEqualTo(1000000));
  });

  test('never overflows, however long the server is down', () {
    final backoff = Backoff(random: Random(1));
    for (var i = 0; i < 1000; i++) {
      expect(backoff.next(), lessThanOrEqualTo(const Duration(seconds: 60)));
    }
  });

  test('spreads delays out, so clients do not all return together', () {
    final delays = {
      for (var seed = 0; seed < 50; seed++)
        (Backoff(random: Random(seed))..next()).next(),
    };
    expect(delays.length, greaterThan(40));
  });

  test('uses the base and cap it is given', () {
    final backoff = Backoff(
      base: const Duration(milliseconds: 10),
      max: const Duration(milliseconds: 40),
      random: Random(1),
    );
    for (var i = 0; i < 10; i++) {
      expect(
        backoff.next(),
        lessThanOrEqualTo(const Duration(milliseconds: 40)),
      );
    }
  });
}
