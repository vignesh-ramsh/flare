import 'package:app/data/providers.dart';
import 'package:app/features/add/add_screen.dart';
import 'package:app/features/thread/thread_pane.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'helpers.dart';

final _server = find.widgetWithText(TextFormField, 'Server');
final _topic = find.widgetWithText(TextFormField, 'Topic');
final _submit = find.widgetWithText(FilledButton, 'Subscribe');

Future<List<String>> _topicNames(WidgetTester tester, dynamic db) async {
  final rows = await tester.runAsync(
    () => db.select(db.topics).get() as Future<List<dynamic>>,
  );
  return [for (final row in rows!) '${row.name}'];
}

void main() {
  group('Subscribe', () {
    testApp('opens as a bottom sheet in a compact window', (tester, db) async {
      await pumpApp(tester, db, width: 390);
      await tester.tap(find.byType(FloatingActionButton));
      await settle(tester);

      expect(find.byType(BottomSheet), findsOneWidget);
      expect(find.byType(Dialog), findsNothing);
      expect(find.byType(AddScreen), findsOneWidget);
    });

    testApp('opens as a bottom sheet in a medium window too', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 700);
      await tester.tap(find.byType(FloatingActionButton));
      await settle(tester);

      expect(find.byType(BottomSheet), findsOneWidget);
    });

    testApp('starts with the server the user already uses', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');

      final field = tester.widget<TextFormField>(_server);
      expect(field.controller!.text, 'https://example.com');
    });

    testApp('subscribes, closes, and opens the new topic', (tester, db) async {
      await pumpApp(tester, db, width: 390);
      await tester.tap(find.byType(FloatingActionButton));
      await settle(tester);

      await tester.enterText(_topic, 'new-topic');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.byType(AddScreen), findsNothing);
      expect(await _topicNames(tester, db), contains('new-topic'));
      expect(find.text('new-topic'), findsOneWidget);
      expect(
        find.text('Nothing has been published to this topic yet.'),
        findsOneWidget,
      );
      expect(find.byType(ThreadPane), findsOneWidget);
    });

    testApp('adds a server the user types, with its topic', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');

      await tester.enterText(_server, 'Push.Example.org:8443');
      await tester.enterText(_topic, 'ops');
      await tester.tap(_submit);
      await settle(tester);

      final servers = await tester.runAsync(() => db.select(db.servers).get());
      expect(
        servers!.map((s) => (s.host, s.url)),
        contains(('push.example.org:8443', 'https://push.example.org:8443')),
      );
      expect(find.text('push.example.org:8443'), findsWidgets);
    });

    testApp('asks for a topic', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.text('Enter a topic'), findsOneWidget);
      expect(find.byType(AddScreen), findsOneWidget);
    });

    testApp('rejects a topic the server would refuse', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_topic, 'has space');
      await tester.tap(_submit);
      await settle(tester);

      expect(
        find.text('Use 1 to 64 letters, digits, dashes or underscores'),
        findsOneWidget,
      );
      expect(await _topicNames(tester, db), isNot(contains('has space')));
    });

    testApp('rejects a server that is not a web address', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_server, 'ftp://example.com');
      await tester.enterText(_topic, 'ok');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.textContaining('That is not a web address'), findsOneWidget);
      expect(await _topicNames(tester, db), isNot(contains('ok')));
    });

    testApp('asks for a server when the field is empty', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_server, '');
      await tester.enterText(_topic, 'ok');
      await tester.tap(_submit);
      await settle(tester);

      expect(find.text("Enter the server's address"), findsOneWidget);
    });

    testApp('says so when already subscribed, and stays open', (
      tester,
      db,
    ) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_topic, 'alerts-db');
      await tester.tap(_submit);
      await settle(tester);

      expect(
        find.text('You are already subscribed to this topic'),
        findsOneWidget,
      );
      expect(find.byType(AddScreen), findsOneWidget);
      expect(
        (await _topicNames(tester, db)).where((n) => n == 'alerts-db'),
        hasLength(1),
      );
    });

    testApp('clears that message when the topic changes', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_topic, 'alerts-db');
      await tester.tap(_submit);
      await settle(tester);

      await tester.enterText(_topic, 'alerts-db2');
      await tester.tap(_submit);
      await settle(tester);

      expect(
        find.text('You are already subscribed to this topic'),
        findsNothing,
      );
      expect(await _topicNames(tester, db), contains('alerts-db2'));
    });

    testApp('submits from the keyboard', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/add');
      await tester.enterText(_topic, 'from-keyboard');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await settle(tester);

      expect(await _topicNames(tester, db), contains('from-keyboard'));
    });

    testApp('cancel closes it and adds nothing', (tester, db) async {
      await pumpApp(tester, db, width: 390);
      await tester.tap(find.byType(FloatingActionButton));
      await settle(tester);
      await tester.enterText(_topic, 'never');
      await tester.tap(find.text('Cancel'));
      await settle(tester);

      expect(find.byType(AddScreen), findsNothing);
      expect(await _topicNames(tester, db), isNot(contains('never')));
    });

    testApp(
      'a build locked to one server hides the server field and uses that server',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: '/add',
          overrides: [
            serverPolicyProvider.overrideWithValue(
              const ServerPolicy(
                defaultUrl: 'https://push.example.org',
                allowCustom: false,
              ),
            ),
          ],
        );

        expect(_server, findsNothing);
        await tester.enterText(_topic, 'locked');
        await tester.tap(_submit);
        await settle(tester);

        final servers = await tester.runAsync(
          () => db.select(db.servers).get(),
        );
        expect(servers!.map((s) => s.host), contains('push.example.org'));
      },
    );
  });
}
