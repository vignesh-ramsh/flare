// Notifications on the real platform: the app, the real server and, on Linux, a real
// notification daemon (dunst) on a real D-Bus session. Nothing is replaced, so this proves a
// message becomes a notification the desktop actually shows.
//
//   make server
//   make app-notifications
//
// Needs `dbus-run-session`, `xvfb-run` and `dunst`; `make app-notifications` runs them.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:app/main.dart' as app;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import '../test/integration/real_server.dart';
import 'helpers.dart';

/// What dunst has closed, newest first. `timeout` is 0 for one that never expires by itself,
/// which is how a critical notification shows (dunst's history has no urgency field).
Future<List<({String summary, String body, String category, int timeout})>>
history() async {
  final result = await Process.run('dunstctl', ['history']);
  final data =
      (jsonDecode(result.stdout as String) as Map<String, dynamic>)['data']
          as List;
  return [
    for (final entry in (data.isEmpty ? const [] : data.first as List))
      (
        summary: (entry['summary'] as Map)['data'] as String,
        body: (entry['body'] as Map)['data'] as String,
        category: (entry['category'] as Map)['data'] as String,
        timeout: (entry['timeout'] as Map)['data'] as int,
      ),
  ];
}

Future<int> shownCount() async {
  var total = 0;
  for (final kind in ['displayed', 'waiting']) {
    final result = await Process.run('dunstctl', ['count', kind]);
    total += int.parse((result.stdout as String).trim());
  }
  return total;
}

/// Moves what dunst is showing into its history, so it can be read.
Future<void> closeAll() async {
  await Process.run('dunstctl', ['close-all']);
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets(
    'a message becomes a notification, and the user\'s choices decide when',
    (tester) async {
      final server = await RealServer.start();
      addTearDown(server.dispose);
      // History the server already holds: it must not notify when the topic is first followed.
      await server.publish('alerts', 'an old one');
      await server.publish('alerts', 'an older one');

      unawaited(app.main());
      await chooseServerAndSkipSignIn(tester, server.url.toString());

      // Subscribe: the thread opens, with the history in it and no notification for it.
      await pumpUntil(tester, find.text('No topics yet'));
      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe').first);
      await pumpUntil(tester, find.widgetWithText(TextFormField, 'Topic'));
      await tester.enterText(
        find.widgetWithText(TextFormField, 'Topic'),
        'alerts',
      );
      await tester.tap(find.widgetWithText(FilledButton, 'Subscribe').last);
      await pumpUntil(tester, find.text('an old one'));
      expect(await shownCount(), 0, reason: 'history is listed, not announced');

      // While the thread is open and focused, a new message is read as it arrives.
      await server.publish('alerts', 'while you look', title: 'Seen');
      await pumpUntil(tester, find.text('Seen'));
      await pumpFor(tester, const Duration(seconds: 2));
      expect(await shownCount(), 0, reason: 'the user is reading this thread');

      // Away from the thread, the same kind of message is announced.
      await tester.tap(find.text('Inbox'));
      await pumpUntil(tester, find.text('Seen'));
      await server.publish(
        'alerts',
        'db-01 /var is at 92%',
        title: 'Disk almost full',
      );
      await _until(
        tester,
        () async => await shownCount() >= 1,
        'a notification for the message',
      );
      await closeAll();
      final first = (await history()).first;
      expect(first.summary, 'Disk almost full');
      expect(
        first.body,
        contains('alerts on ${server.url.host}:${server.url.port}'),
      );
      expect(first.body, contains('db-01 /var is at 92%'));
      expect(first.category, 'im.received');
      expect(
        first.timeout,
        isNot(0),
        reason: 'an ordinary notification expires by itself',
      );

      // A high priority message says so, and stays until dismissed.
      await server.publish(
        'alerts',
        'writes will fail',
        title: 'Disk critical',
        priority: 4,
      );
      await _until(
        tester,
        () async => await shownCount() >= 1,
        'a notification for the high priority one',
      );
      await closeAll();
      final high = (await history()).first;
      expect(high.summary, 'High · Disk critical');
      expect(
        high.timeout,
        0,
        reason: 'a high priority notification stays until dismissed',
      );

      // A minimum priority message is only listed.
      final before = (await history()).length;
      await server.publish(
        'alerts',
        'barely news',
        title: 'Quiet one',
        priority: 1,
      );
      await pumpUntil(tester, find.text('Quiet one'));
      await pumpFor(tester, const Duration(seconds: 2));
      expect(await shownCount(), 0);
      expect((await history()).length, before);

      // Muting the topic silences it, however urgent the message.
      await tester.tap(
        find.text('alerts on ${server.url.host}:${server.url.port}').first,
      );
      await pumpUntil(tester, find.byTooltip('Notifications for this topic'));
      await tester.tap(find.byTooltip('Notifications for this topic'));
      await pumpUntil(tester, find.text('Mute this topic'));
      await tester.tap(find.widgetWithText(SwitchListTile, 'Mute this topic'));
      await tester.pump(const Duration(milliseconds: 500));
      await tester.tap(find.text('Done'));
      await tester.pump(const Duration(milliseconds: 500));
      await tester.tap(find.text('Inbox'));
      await pumpUntil(tester, find.text('Quiet one'));
      await server.publish(
        'alerts',
        'still there',
        title: 'Muted but urgent',
        priority: 5,
      );
      await pumpUntil(tester, find.text('Muted but urgent'));
      await pumpFor(tester, const Duration(seconds: 2));
      expect(await shownCount(), 0, reason: 'the topic is muted');
    },
    timeout: const Timeout(Duration(minutes: 4)),
    skip: serverBinary == null || !Platform.isLinux,
  );
}

Future<void> _until(
  WidgetTester tester,
  Future<bool> Function() condition,
  String what,
) async {
  final deadline = DateTime.now().add(const Duration(seconds: 20));
  while (!await condition()) {
    if (DateTime.now().isAfter(deadline)) {
      throw TestFailure('Timed out waiting for $what');
    }
    await tester.pump(const Duration(milliseconds: 200));
  }
}
