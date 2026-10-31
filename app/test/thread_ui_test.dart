import 'dart:async';
import 'dart:convert';

import 'package:app/core/db/database.dart';
import 'package:app/data/topics_repository.dart';
import 'package:drift/drift.dart' show Value;
import 'package:app/features/thread/composer.dart';
import 'package:app/features/thread/message_bubble.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/ui/message_card.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'data/test_db.dart';
import 'helpers.dart';
import 'net/spy_manager.dart';

Future<void> onPlatform(
  TargetPlatform platform,
  Future<void> Function() body,
) async {
  debugDefaultTargetPlatformOverride = platform;
  try {
    await body();
  } finally {
    debugDefaultTargetPlatformOverride = null;
  }
}

void main() {
  late List<http.Request> requests;
  late FutureOr<http.Response> Function(http.Request) answer;
  var nextId = 100;

  http.Response stored(http.Request request) {
    final body = jsonDecode(request.body) as Map<String, dynamic>;
    return http.Response(
      jsonEncode(
        {
          'id': mid(nextId++),
          'time': testNow.millisecondsSinceEpoch ~/ 1000,
          'event': 'message',
          'topic': body['topic'],
          'title': body['title'],
          'message': body['message'],
          'priority': body['priority'],
        }..removeWhere((key, value) => value == null),
      ),
      200,
    );
  }

  http.Response error(int status, int code) => http.Response(
    jsonEncode({'code': code, 'http': status, 'error': 'no'}),
    status,
  );

  setUp(() {
    requests = [];
    answer = stored;
    nextId = 100;
  });

  List<Override> overrides(AppDatabase db) => [
    apiProvider.overrideWithValue(
      ServerApi(
        client: MockClient((request) async {
          requests.add(request);
          return answer(request);
        }),
      ),
    ),
    connectionManagerProvider.overrideWithValue(
      SpyManager(TopicsRepository(db)),
    ),
  ];

  Future<void> open(
    WidgetTester tester,
    AppDatabase db, {
    double width = 390,
    String topic = 'alerts-db',
  }) => pumpApp(
    tester,
    db,
    width: width,
    location: '/topic/example.com/$topic',
    overrides: overrides(db),
  );

  final input = find.byWidgetPredicate(
    (w) => w is TextField && w.decoration?.hintText == 'Write a message',
  );
  // The button itself: `byTooltip` finds the tooltip around it, which cannot be asked `onPressed`.
  final send = find.byWidgetPredicate(
    (w) => w is IconButton && w.tooltip == 'Send',
  );

  Future<void> type(WidgetTester tester, String text) async {
    await tester.enterText(input, text);
    await tester.pump();
  }

  group('the composer', () {
    testApp('is under the messages on every layout', (tester, db) async {
      for (final width in [390.0, 700.0, 1000.0, 1400.0]) {
        await open(tester, db, width: width);
        expect(find.byType(Composer), findsOneWidget, reason: '$width');
        expect(input, findsOneWidget, reason: '$width');
        await tester.pumpWidget(const SizedBox.shrink());
      }
    });

    testApp('is not there for a topic the user is not subscribed to', (
      tester,
      db,
    ) async {
      await open(tester, db, topic: 'nope');
      expect(find.byType(Composer), findsNothing);
    });

    testApp('cannot send nothing', (tester, db) async {
      await open(tester, db);
      expect(tester.widget<IconButton>(send).onPressed, isNull);

      await type(tester, '   ');
      expect(tester.widget<IconButton>(send).onPressed, isNull);

      await type(tester, 'hi');
      expect(tester.widget<IconButton>(send).onPressed, isNotNull);
    });

    testApp(
      'sends, empties the box, and shows the message as the user\'s own',
      (tester, db) async {
        await open(tester, db);
        await type(tester, '  on my way  ');
        await tester.tap(send);
        await settle(tester);

        expect(requests.single.url.path, '/v1/messages');
        expect(jsonDecode(requests.single.body), {
          'topic': 'alerts-db',
          'message': 'on my way',
        });
        expect(tester.widget<TextField>(input).controller!.text, isEmpty);
        expect(find.byType(MessageBubble), findsOneWidget);
        expect(
          find.descendant(
            of: find.byType(MessageBubble),
            matching: find.text('on my way'),
          ),
          findsOneWidget,
        );
        // The others' messages are still cards.
        expect(find.byType(MessageCard), findsNWidgets(3));
      },
    );

    testApp(
      'puts the user\'s message on the right, and the others\' on the left',
      (tester, db) async {
        await open(tester, db);
        await type(tester, 'mine');
        await tester.tap(send);
        await settle(tester);

        // The bubble itself: the widget around it spans the whole row.
        final bubble = tester.getRect(
          find
              .descendant(
                of: find.byType(MessageBubble),
                matching: find.byType(DecoratedBox),
              )
              .first,
        );
        final card = tester.getRect(find.byType(MessageCard).first);
        expect(bubble.right, greaterThan(card.right - 1));
        expect(bubble.left, greaterThan(card.left + 40));
      },
    );

    testApp(
      'sends a title and a priority from the options, then goes back to the plain form',
      (tester, db) async {
        await open(tester, db);
        expect(find.text('Title (optional)'), findsNothing);
        await tester.tap(find.byTooltip('Title and priority'));
        await tester.pump();

        await tester.enterText(
          find.widgetWithText(TextField, 'Title (optional)'),
          'Heads up',
        );
        await tester.tap(find.byType(DropdownButtonFormField<int>));
        await settle(tester);
        await tester.tap(find.text('High').last);
        await settle(tester);
        await type(tester, 'the disk is full');
        await tester.tap(send);
        await settle(tester);

        expect(jsonDecode(requests.single.body), {
          'topic': 'alerts-db',
          'message': 'the disk is full',
          'title': 'Heads up',
          'priority': 4,
        });
        expect(
          tester
              .widget<TextField>(
                find.widgetWithText(TextField, 'Title (optional)'),
              )
              .controller!
              .text,
          isEmpty,
        );
        expect(
          tester
              .widget<DropdownButtonFormField<int>>(
                find.byType(DropdownButtonFormField<int>),
              )
              .initialValue,
          3,
        );
        // The bubble says what it was.
        expect(
          find.descendant(
            of: find.byType(MessageBubble),
            matching: find.text('Heads up'),
          ),
          findsOneWidget,
        );
        expect(
          find.descendant(
            of: find.byType(MessageBubble),
            matching: find.text('High'),
          ),
          findsOneWidget,
        );
      },
    );

    testApp('does not send twice while waiting', (tester, db) async {
      final gate = Completer<http.Response>();
      answer = (_) => gate.future;
      await open(tester, db);
      await type(tester, 'once');
      await tester.tap(send);
      await tester.pump();

      expect(tester.widget<IconButton>(send).onPressed, isNull);
      await tester.tap(send, warnIfMissed: false);
      expect(requests, hasLength(1));

      gate.complete(stored(requests.single));
      await settle(tester);
      expect(find.byType(MessageBubble), findsOneWidget);
    });

    testApp('Enter sends on a desktop, and starts a new line with Shift', (
      tester,
      db,
    ) async {
      await onPlatform(TargetPlatform.linux, () async {
        await open(tester, db, width: 1000);
        await tester.tap(input);
        await tester.enterText(input, 'line one');
        await tester.pump();

        await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
        await tester.pump();
        expect(requests, isEmpty, reason: 'Shift+Enter is not "send"');

        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await settle(tester);
        expect(requests, hasLength(1));
      });
    });

    testApp('Enter does not send on a phone, where the button sends', (
      tester,
      db,
    ) async {
      await open(tester, db);
      await tester.tap(input);
      await tester.enterText(input, 'hello');
      await tester.pump();
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pump();
      expect(requests, isEmpty);
    });

    testApp('meets the accessibility guidelines', (tester, db) async {
      final handle = tester.ensureSemantics();
      await open(tester, db);
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      handle.dispose();
    });
  });

  group('when sending fails', () {
    Future<void> trySending(WidgetTester tester, AppDatabase db) async {
      await open(tester, db);
      await type(tester, 'important');
      await tester.tap(send);
      await settle(tester);
    }

    testApp(
      'says the server wants a sign-in, offers it, and keeps what was typed',
      (tester, db) async {
        answer = (_) => error(401, 40101);
        await trySending(tester, db);

        expect(
          find.text(
            'example.com needs you to sign in before you can send here.',
          ),
          findsOneWidget,
        );
        expect(tester.widget<TextField>(input).controller!.text, 'important');
        expect(find.byType(MessageBubble), findsNothing);

        await tester.tap(find.widgetWithText(TextButton, 'Sign in'));
        await settle(tester);
        expect(find.text('Sign in to example.com'), findsOneWidget);
      },
    );

    testApp('says the user is not allowed to send, with no sign-in offer', (
      tester,
      db,
    ) async {
      answer = (_) => error(403, 40301);
      await trySending(tester, db);

      expect(
        find.text('You are not allowed to send to this topic.'),
        findsOneWidget,
      );
      expect(find.widgetWithText(TextButton, 'Sign in'), findsNothing);
    });

    testApp('says the message is too long', (tester, db) async {
      answer = (_) => error(413, 41301);
      await trySending(tester, db);
      expect(
        find.text('That message is too long for this server.'),
        findsOneWidget,
      );
    });

    testApp('says the server cannot be reached', (tester, db) async {
      answer = (_) => throw http.ClientException('down');
      await trySending(tester, db);
      expect(find.textContaining("Can't reach example.com"), findsOneWidget);
    });

    testApp(
      'lets the user send again once the cause is fixed, and the error goes away',
      (tester, db) async {
        answer = (_) => throw http.ClientException('down');
        await trySending(tester, db);
        expect(find.textContaining("Can't reach"), findsOneWidget);

        answer = stored;
        await tester.tap(send);
        await settle(tester);

        expect(find.textContaining("Can't reach"), findsNothing);
        expect(find.byType(MessageBubble), findsOneWidget);
        expect(tester.widget<TextField>(input).controller!.text, isEmpty);
      },
    );
  });

  group('the user\'s own messages', () {
    testApp('are a bubble in the thread and a card in the inbox', (
      tester,
      db,
    ) async {
      await tester.runAsync(() async {
        await db
            .into(db.messages)
            .insert(
              MessagesCompanion.insert(
                serverId: 1,
                id: mid(50),
                topic: 'alerts-db',
                time: testNow.millisecondsSinceEpoch ~/ 1000,
                body: const Value('sent by me'),
                mine: const Value(true),
              ),
            );
      });
      await open(tester, db);
      expect(find.byType(MessageBubble), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());

      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/inbox',
        overrides: overrides(db),
      );
      expect(find.byType(MessageBubble), findsNothing);
      expect(find.text('sent by me'), findsOneWidget);
    });

    testApp('are named for a screen reader', (tester, db) async {
      final handle = tester.ensureSemantics();
      await tester.runAsync(() async {
        await db
            .into(db.messages)
            .insert(
              MessagesCompanion.insert(
                serverId: 1,
                id: mid(50),
                topic: 'alerts-db',
                time: 1,
                body: const Value('sent by me'),
                mine: const Value(true),
              ),
            );
      });
      await open(tester, db);
      expect(find.bySemanticsLabel(RegExp('^You')), findsOneWidget);
      handle.dispose();
    });
  });
}
