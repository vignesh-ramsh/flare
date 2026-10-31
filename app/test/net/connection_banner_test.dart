import 'package:app/core/db/database.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/net/connection_manager.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/server_status.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';

import '../helpers.dart';

/// Counts calls to reconnectAll instead of touching the network.
class SpyManager extends ConnectionManager {
  SpyManager(TopicsRepository topics)
    : super(topics: topics, factory: (_) => throw StateError('not used'));

  int reconnects = 0;

  @override
  void reconnectAll() => reconnects++;
}

void main() {
  /// Runs the app with these connection statuses for the seeded server (ID 1).
  Future<SpyManager> pumpWithStatus(
    WidgetTester tester,
    AppDatabase db,
    ServerStatus status, {
    double width = 390,
    String location = '/',
    int serverId = 1,
  }) async {
    final spy = SpyManager(TopicsRepository(db));
    final List<Override> overrides = [
      connectionManagerProvider.overrideWithValue(spy),
      connectionStatusProvider.overrideWith(
        (ref) => Stream.value({serverId: status}),
      ),
    ];
    await pumpApp(
      tester,
      db,
      width: width,
      location: location,
      overrides: overrides,
    );
    return spy;
  }

  ServerStatus waiting({int seconds = 8}) => ServerStatus(
    state: LinkState.waiting,
    retryAt: testNow.add(Duration(seconds: seconds)),
    failures: 2,
  );

  group('server banner', () {
    testApp('says the server cannot be reached and when it will try again', (
      tester,
      db,
    ) async {
      await pumpWithStatus(tester, db, waiting());

      expect(
        find.text("Can't reach example.com. Trying again in 8 seconds."),
        findsOneWidget,
      );
      expect(find.text('Try now'), findsOneWidget);
    });

    testApp('says "1 second" in the singular', (tester, db) async {
      await pumpWithStatus(tester, db, waiting(seconds: 1));
      expect(find.textContaining('Trying again in 1 second.'), findsOneWidget);
    });

    testApp('says it is trying again once the wait is over', (
      tester,
      db,
    ) async {
      await pumpWithStatus(tester, db, waiting(seconds: 0));
      expect(
        find.text("Can't reach example.com. Trying again."),
        findsOneWidget,
      );
    });

    testApp('says the saved sign-in was not accepted', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        const ServerStatus(state: LinkState.authFailed),
      );
      expect(
        find.text('example.com did not accept the saved sign-in.'),
        findsOneWidget,
      );
    });

    testApp('says nothing while connecting, connected or idle', (
      tester,
      db,
    ) async {
      for (final state in [
        LinkState.connecting,
        LinkState.connected,
        LinkState.idle,
      ]) {
        await pumpWithStatus(tester, db, ServerStatus(state: state));
        expect(find.byIcon(Icons.cloud_off), findsNothing, reason: '$state');
        await tester.pumpWidget(const SizedBox.shrink());
      }
    });

    testApp('Try now asks the manager to reconnect', (tester, db) async {
      final spy = await pumpWithStatus(tester, db, waiting());
      await tester.tap(find.text('Try now'));
      await settle(tester);
      expect(spy.reconnects, 1);
    });

    testApp('shows in the thread too, because the thread covers the list', (
      tester,
      db,
    ) async {
      await pumpWithStatus(
        tester,
        db,
        waiting(),
        location: '/topic/example.com/alerts-db',
      );
      expect(find.textContaining("Can't reach example.com"), findsOneWidget);
    });

    testApp(
      'shows once in the large layout, in the sidebar, even with a thread open',
      (tester, db) async {
        await pumpWithStatus(
          tester,
          db,
          waiting(),
          width: 1400,
          location: '/topic/example.com/alerts-db',
        );
        expect(find.textContaining("Can't reach example.com"), findsOneWidget);
      },
    );

    testApp(
      'shows once in the expanded layout, in the list, even with a thread open',
      (tester, db) async {
        await pumpWithStatus(
          tester,
          db,
          waiting(),
          width: 1000,
          location: '/topic/example.com/alerts-db',
        );
        expect(find.textContaining("Can't reach example.com"), findsOneWidget);
      },
    );

    testApp('shows in the large layout with no thread open', (
      tester,
      db,
    ) async {
      await pumpWithStatus(tester, db, waiting(), width: 1400);
      expect(find.textContaining("Can't reach example.com"), findsOneWidget);
    });

    testApp('says nothing about a server that is not in the list', (
      tester,
      db,
    ) async {
      await pumpWithStatus(tester, db, waiting(), serverId: 99);
      expect(find.byIcon(Icons.cloud_off), findsNothing);
    });

    testApp('meets the contrast guideline', (tester, db) async {
      final handle = tester.ensureSemantics();
      await pumpWithStatus(tester, db, waiting());
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      handle.dispose();
    });
  });

  group('refused topic', () {
    ServerStatus refused(int code) => ServerStatus(
      state: LinkState.connected,
      topics: {'alerts-db': TopicLink(errorCode: code)},
    );

    testApp('says the topic needs a sign-in', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        refused(40101),
        location: '/topic/example.com/alerts-db',
      );
      expect(
        find.text('example.com needs you to sign in to read this topic.'),
        findsOneWidget,
      );
    });

    testApp('says access is not allowed', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        refused(40301),
        location: '/topic/example.com/alerts-db',
      );
      expect(find.text("You don't have access to this topic."), findsOneWidget);
    });

    testApp('names the error for any other refusal', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        refused(50001),
        location: '/topic/example.com/alerts-db',
      );
      expect(
        find.text('example.com refused this topic (error 50001).'),
        findsOneWidget,
      );
    });

    testApp('says nothing about a topic that is live', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        const ServerStatus(
          state: LinkState.connected,
          topics: {'alerts-db': TopicLink(live: true)},
        ),
        location: '/topic/example.com/alerts-db',
      );
      expect(find.byIcon(Icons.lock_outline), findsNothing);
    });

    testApp('says nothing on another topic', (tester, db) async {
      await pumpWithStatus(
        tester,
        db,
        refused(40301),
        location: '/topic/example.com/backups',
      );
      expect(find.byIcon(Icons.lock_outline), findsNothing);
    });
  });
}
