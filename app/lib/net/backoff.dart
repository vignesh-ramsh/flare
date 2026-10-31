import 'dart:math';

/// How long to wait before the next attempt to reconnect: doubling from [base] up to
/// [max], with half of each delay randomised so that many apps whose server just
/// restarted do not all come back at the same instant.
class Backoff {
  Backoff({
    this.base = const Duration(seconds: 1),
    this.max = const Duration(seconds: 60),
    Random? random,
  }) : _random = random ?? Random();

  final Duration base;
  final Duration max;
  final Random _random;
  int _failures = 0;

  /// Consecutive failures counted so far.
  int get failures => _failures;

  /// The delay after one more failure.
  Duration next() {
    // Past 2^20 times the base the cap has long applied; stopping here avoids overflow.
    final doublings = min(_failures, 20);
    final ceiling = base * (1 << doublings);
    final capped = ceiling > max ? max : ceiling;
    _failures++;
    final jitter = 0.5 + _random.nextDouble() / 2;
    return Duration(microseconds: (capped.inMicroseconds * jitter).round());
  }

  void reset() => _failures = 0;
}
