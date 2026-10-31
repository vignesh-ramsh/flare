import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Pumps until [finder] matches, or fails after [timeout]. Real time passes, because the app
/// talks to a real server and a real notification daemon.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  Duration timeout = const Duration(seconds: 30),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (finder.evaluate().isEmpty) {
    if (DateTime.now().isAfter(deadline)) {
      throw TestFailure('Timed out waiting for $finder');
    }
    await tester.pump(const Duration(milliseconds: 100));
  }
  await tester.pump(const Duration(milliseconds: 300));
}

/// Pumps real time for [duration], for proving that something does not happen.
Future<void> pumpFor(WidgetTester tester, Duration duration) async {
  final end = DateTime.now().add(duration);
  while (DateTime.now().isBefore(end)) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

/// Chooses a server by its address on the first-run pages, then skips signing in.
Future<void> chooseServerAndSkipSignIn(
  WidgetTester tester,
  String address,
) async {
  await pumpUntil(tester, find.text('Get started'));
  await tester.tap(find.text('Get started'));
  await pumpUntil(tester, find.text('Choose a server'));
  await tester.enterText(find.widgetWithText(TextFormField, 'Server'), address);
  await tester.tap(find.text('Check server'));
  await pumpUntil(tester, find.textContaining('Found'));
  await tester.tap(find.text('Continue'));
  await pumpUntil(tester, find.text('Skip for now'));
  await tester.tap(find.text('Skip for now'));
}
