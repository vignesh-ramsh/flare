import 'dart:async';

import 'package:app/app/app.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/models.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/settings_repository.dart';
import 'package:app/data/startup.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/desktop/desktop_services.dart' show windowActionsProvider;
import 'package:app/desktop/window_actions.dart';
import 'package:app/net/connection_lifecycle.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/theme_sync.dart';
import 'package:app/notifications/providers.dart';
import 'package:app/notifications/viewing.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'data/test_db.dart';
import 'helpers.dart';
import 'net/spy_manager.dart';
import 'notifications/fakes.dart';

import 'package:app/notifications/notification_sink.dart';

/// Runs [body] as if on [platform]. The override has to be cleared before the test ends, which
/// is before any tear-down runs.
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
  Future<NotificationPrefs> prefsOf(
    WidgetTester tester,
    AppDatabase db,
  ) async => (await tester.runAsync(() => AppSettingsRepository(db).read()))!;

  group('Settings > Notifications', () {
    testApp('is reached from Settings', (tester, db) async {
      await pumpApp(tester, db, width: 390, location: '/settings');
      await tester.tap(find.text('Notifications'));
      await settle(tester);

      expect(find.text('Show notifications'), findsOneWidget);
      expect(find.byType(BackButton), findsOneWidget);
    });

    testApp('shows what is on, and changes it', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings/notifications',
      );
      expect(
        tester
            .widgetList<SwitchListTile>(find.byType(SwitchListTile))
            .map((s) => s.value),
        everyElement(isTrue),
      );

      await tester.tap(find.widgetWithText(SwitchListTile, 'Play a sound'));
      await settle(tester);
      await tester.tap(
        find.widgetWithText(
          SwitchListTile,
          'Show the message in notifications',
        ),
      );
      await settle(tester);

      final prefs = await prefsOf(tester, db);
      expect(
        (prefs.enabled, prefs.sound, prefs.showContent),
        (true, false, false),
      );
      expect(
        tester
            .widget<SwitchListTile>(
              find.widgetWithText(SwitchListTile, 'Play a sound'),
            )
            .value,
        isFalse,
      );
    });

    testApp(
      'turning notifications off leaves the other choices alone but unreachable',
      (tester, db) async {
        await pumpApp(
          tester,
          db,
          width: 390,
          location: '/settings/notifications',
        );
        await tester.tap(
          find.widgetWithText(SwitchListTile, 'Show notifications'),
        );
        await settle(tester);

        expect((await prefsOf(tester, db)).enabled, isFalse);
        expect(
          tester
              .widget<SwitchListTile>(
                find.widgetWithText(SwitchListTile, 'Play a sound'),
              )
              .onChanged,
          isNull,
        );
        expect(
          tester
              .widget<SwitchListTile>(
                find.widgetWithText(
                  SwitchListTile,
                  'Show the message in notifications',
                ),
              )
              .onChanged,
          isNull,
        );
        expect(
          find.widgetWithText(TextButton, 'Pause for 1 hour'),
          findsOneWidget,
        );
        expect(
          tester
              .widget<TextButton>(
                find.widgetWithText(TextButton, 'Pause for 1 hour'),
              )
              .onPressed,
          isNull,
        );
      },
    );

    testApp('pauses for an hour, says until when, and resumes', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings/notifications',
      );
      await tester.tap(find.widgetWithText(TextButton, 'Pause for 1 hour'));
      await settle(tester);

      expect(
        (await prefsOf(tester, db)).pausedUntil,
        testNow.add(const Duration(hours: 1)),
      );
      expect(find.textContaining('Paused until 1:00'), findsOneWidget);

      await tester.tap(find.text('Resume'));
      await settle(tester);
      expect((await prefsOf(tester, db)).pausedUntil, isNull);
      expect(
        find.widgetWithText(TextButton, 'Pause for 1 hour'),
        findsOneWidget,
      );
    });

    testApp('offers the tray on a desktop, and says when there is none', (
      tester,
      db,
    ) async {
      await onPlatform(TargetPlatform.linux, () async {
        await pumpApp(
          tester,
          db,
          width: 1000,
          location: '/settings/notifications',
          overrides: [trayAvailableProvider.overrideWith(() => _Tray(true))],
        );
        final close = find.widgetWithText(
          SwitchListTile,
          'Keep running in the tray when the window is closed',
        );
        expect(tester.widget<SwitchListTile>(close).value, isTrue);
        expect(find.textContaining('No system tray'), findsNothing);

        await tester.tap(close);
        await settle(tester);
        expect((await prefsOf(tester, db)).closeToTray, isFalse);
      });
    });

    testApp('says closing the window quits where there is no tray', (
      tester,
      db,
    ) async {
      await onPlatform(TargetPlatform.linux, () async {
        await pumpApp(
          tester,
          db,
          width: 1000,
          location: '/settings/notifications',
          overrides: [trayAvailableProvider.overrideWith(() => _Tray(false))],
        );
        final close = tester.widget<SwitchListTile>(
          find.widgetWithText(
            SwitchListTile,
            'Keep running in the tray when the window is closed',
          ),
        );
        expect(close.value, isFalse);
        expect(close.onChanged, isNull);
        expect(
          find.textContaining('closing the window quits the app'),
          findsOneWidget,
        );
      });
    });

    testApp('has no tray section on a phone', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings/notifications',
      );
      expect(find.textContaining('tray'), findsNothing);
      expect(find.text('On this computer'), findsNothing);
    });

    testApp('meets the accessibility guidelines', (tester, db) async {
      final handle = tester.ensureSemantics();
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/settings/notifications',
      );
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(textContrastGuideline));
      handle.dispose();
    });
  });

  group('a topic\'s notifications', () {
    Future<void> openDialog(WidgetTester tester) async {
      await tester.tap(find.byTooltip('Notifications for this topic'));
      await settle(tester);
    }

    testApp('are muted from the thread', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );
      await openDialog(tester);
      expect(find.text('Mute this topic'), findsOneWidget);

      await tester.tap(find.widgetWithText(SwitchListTile, 'Mute this topic'));
      await settle(tester);

      final context = await tester.runAsync(
        () => TopicsRepository(db).contextOf(1, 'alerts-db'),
      );
      expect(context!.muted, isTrue);
      expect(
        tester
            .widget<SwitchListTile>(
              find.widgetWithText(SwitchListTile, 'Mute this topic'),
            )
            .value,
        isTrue,
      );
    });

    testApp('get a minimum priority', (tester, db) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );
      await openDialog(tester);
      await tester.tap(find.byType(DropdownButtonFormField<int>));
      await settle(tester);
      await tester.tap(find.text('High').last);
      await settle(tester);

      final context = await tester.runAsync(
        () => TopicsRepository(db).contextOf(1, 'alerts-db'),
      );
      expect(context!.minPriority, 4);
    });

    testApp('show what was chosen the next time, per topic', (
      tester,
      db,
    ) async {
      await tester.runAsync(() async {
        final topics = TopicsRepository(db);
        final id = (await topics.contextOf(1, 'alerts-db'))!.topicId;
        await topics.setMuted(id, true);
      });
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );
      await openDialog(tester);
      expect(
        tester
            .widget<SwitchListTile>(
              find.widgetWithText(SwitchListTile, 'Mute this topic'),
            )
            .value,
        isTrue,
      );
      await tester.tap(find.text('Done'));
      await settle(tester);

      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/backups',
      );
      await openDialog(tester);
      expect(
        tester
            .widget<SwitchListTile>(
              find.widgetWithText(SwitchListTile, 'Mute this topic'),
            )
            .value,
        isFalse,
      );
    });

    testApp('cannot be given a minimum priority while muted', (
      tester,
      db,
    ) async {
      await tester.runAsync(() async {
        final topics = TopicsRepository(db);
        await topics.setMuted(
          (await topics.contextOf(1, 'alerts-db'))!.topicId,
          true,
        );
      });
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );
      await openDialog(tester);
      expect(
        tester
            .widget<DropdownButtonFormField<int>>(
              find.byType(DropdownButtonFormField<int>),
            )
            .onChanged,
        isNull,
      );
    });

    testApp('can be reached on every layout', (tester, db) async {
      for (final width in [390.0, 700.0, 1000.0, 1400.0]) {
        await pumpApp(
          tester,
          db,
          width: width,
          location: '/topic/example.com/alerts-db',
        );
        expect(
          find.byTooltip('Notifications for this topic'),
          findsOneWidget,
          reason: '$width',
        );
        await tester.pumpWidget(const SizedBox.shrink());
      }
    });
  });

  group('knowing what the user is looking at', () {
    testApp('remembers the open thread, and forgets it when it closes', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 390,
        location: '/topic/example.com/alerts-db',
      );
      final container = ProviderScope.containerOf(
        tester.element(find.byType(Scaffold).first),
      );
      expect(container.read(viewedTopicProvider), (
        host: 'example.com',
        name: 'alerts-db',
      ));

      await tester.tap(find.byType(BackButton));
      await settle(tester);
      expect(container.read(viewedTopicProvider), isNull);
    });

    testApp('follows the thread when another opens beside it', (
      tester,
      db,
    ) async {
      await pumpApp(
        tester,
        db,
        width: 1000,
        location: '/topic/example.com/alerts-db',
      );
      final container = ProviderScope.containerOf(
        tester.element(find.byType(Scaffold).first),
      );

      await tester.tap(find.widgetWithText(InkWell, 'backups').first);
      await settle(tester);
      expect(container.read(viewedTopicProvider), (
        host: 'example.com',
        name: 'backups',
      ));
    });

    testWidgets('knows whether the window has the user\'s attention', (
      tester,
    ) async {
      final spy = SpyManager(TopicsRepository(memoryDb()));
      late _CountingSync sync;
      final container = ProviderContainer(
        overrides: [
          connectionManagerProvider.overrideWithValue(spy),
          themeSyncProvider.overrideWith((ref) => sync = _CountingSync(ref)),
        ],
      );
      addTearDown(container.dispose);
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: const ConnectionLifecycle(child: SizedBox()),
        ),
      );
      expect(container.read(windowFocusProvider), isTrue);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      expect(container.read(windowFocusProvider), isFalse);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      expect(container.read(windowFocusProvider), isTrue);
      expect(
        spy.reconnects,
        1,
        reason: 'coming back also retries the connection',
      );
      expect(
        sync.refreshes,
        1,
        reason: 'and asks the servers for any new themes',
      );
    });
  });

  group('starting notifications', () {
    late RecordingSink sink;
    late List<String> brought;

    setUp(() {
      sink = RecordingSink();
      brought = [];
      debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    });
    tearDown(() => debugDefaultTargetPlatformOverride = null);

    ProviderContainer containerOn(AppDatabase db) {
      final container = ProviderContainer(
        overrides: [
          databaseProvider.overrideWithValue(db),
          notificationSinkProvider.overrideWithValue(sink),
          windowActionsProvider.overrideWithValue(_Window(brought)),
          connectionManagerProvider.overrideWithValue(
            SpyManager(TopicsRepository(db)),
          ),
        ],
      );
      addTearDown(container.dispose);
      return container;
    }

    test('shows a notification for a message that arrives, end to end through the database', () async {
      final db = memoryDb();
      addTearDown(db.close);
      final container = containerOn(db);
      await TopicsRepository(db)
          .subscribe(ServerAddress.parse('https://example.com')!, 'alerts');
      await startNotifications(container);
      expect(sink.initialized, isTrue);

      await container
          .read(messagesRepositoryProvider)
          .store(incoming(1, 'alerts', 1, title: 'Disk', priority: 4));
      await pumpEventQueue();
      await container.read(notificationServiceProvider).flush();

      expect(sink.shown.single.title, 'High · Disk');
      expect(sink.shown.single.route, '/topic/example.com/alerts');
    });

    test('stays quiet for a muted topic and for history', () async {
      final db = memoryDb();
      addTearDown(db.close);
      final container = containerOn(db);
      await TopicsRepository(db)
          .subscribe(ServerAddress.parse('https://example.com')!, 'alerts');
      await TopicsRepository(db)
          .subscribe(ServerAddress.parse('https://example.com')!, 'quiet');
      await TopicsRepository(db).setMuted(
        (await TopicsRepository(db).contextOf(1, 'quiet'))!.topicId,
        true,
      );
      await startNotifications(container);

      final messages = container.read(messagesRepositoryProvider);
      await messages.store(incoming(1, 'quiet', 1, priority: 5));
      await messages.store(
        const IncomingMessage(
          serverId: 1,
          id: 'h',
          topic: 'alerts',
          time: 1,
          silent: true,
        ),
      );
      await container.read(notificationServiceProvider).flush();

      expect(sink.shown, isEmpty);
    });

    test(
      'tapping one opens its thread and brings the window forward',
      () async {
        final db = memoryDb();
        addTearDown(db.close);
        final container = containerOn(db);
        await startNotifications(container);

        sink.onTap!('/topic/example.com/alerts');

        final router = container.read(routerProvider);
        expect(
          router.routeInformationProvider.value.uri.path,
          '/topic/example.com/alerts',
        );
        expect(brought, ['front']);
      },
    );

    test('the app still starts when the system\'s notification service cannot be reached', () async {
      final db = memoryDb();
      addTearDown(db.close);
      final container = containerOn(db);
      await TopicsRepository(db)
          .subscribe(ServerAddress.parse('https://example.com')!, 'alerts');
      final failing = _FailingSink();
      final broken = ProviderContainer(
        overrides: [
          databaseProvider.overrideWithValue(db),
          notificationSinkProvider.overrideWithValue(failing),
          connectionManagerProvider.overrideWithValue(
            SpyManager(TopicsRepository(db)),
          ),
        ],
      );
      addTearDown(broken.dispose);

      await startNotifications(broken); // does not throw
      await broken
          .read(messagesRepositoryProvider)
          .store(incoming(1, 'alerts', 1));
      await broken.read(notificationServiceProvider).flush();
      expect(failing.shown, isEmpty);
      container.dispose();
    });

    test('an error the notification service raises on its own is not left unhandled', () async {
      final db = memoryDb();
      addTearDown(db.close);
      final leaky = _LeakySink();
      final broken = ProviderContainer(
        overrides: [
          databaseProvider.overrideWithValue(db),
          notificationSinkProvider.overrideWithValue(leaky),
          connectionManagerProvider.overrideWithValue(
            SpyManager(TopicsRepository(db)),
          ),
        ],
      );
      addTearDown(broken.dispose);

      // A test fails on any error nobody handled, so passing is the proof.
      await startNotifications(broken);
      await Future<void>.delayed(const Duration(milliseconds: 50));
    });

    test(
      'does nothing on a phone, which the system\'s push service wakes instead',
      () async {
        debugDefaultTargetPlatformOverride = TargetPlatform.android;
        final db = memoryDb();
        addTearDown(db.close);
        final container = containerOn(db);
        await startNotifications(container);
        expect(sink.initialized, isFalse);
      },
    );
  });
}

class _Tray extends TrayAvailable {
  _Tray(this._value);
  final bool _value;

  @override
  bool? build() => _value;
}

class _Window implements WindowActions {
  _Window(this.calls);
  final List<String> calls;

  @override
  Future<void> bringToFront() async => calls.add('front');

  @override
  Future<void> hide() async => calls.add('hide');

  @override
  Future<void> quit() async => calls.add('quit');
}

/// What the Linux plugin does without a session bus: the failure reaches the caller, and also
/// comes out of a stream listener where nobody can catch it.
class _LeakySink extends RecordingSink {
  @override
  Future<void> initialize(NotificationTapped onTap) async {
    unawaited(
      Future<void>.error(StateError('no D-Bus session bus (listener)')),
    );
    throw StateError('no D-Bus session bus');
  }
}

/// A theme sync that only counts how often it was asked to refresh.
class _CountingSync extends ThemeSync {
  _CountingSync(super.ref);

  int refreshes = 0;

  @override
  Future<void> refreshAll({
    Duration within = const Duration(minutes: 1),
  }) async => refreshes++;
}

class _FailingSink extends RecordingSink {
  @override
  Future<void> initialize(NotificationTapped onTap) =>
      Future.error(StateError('no D-Bus session bus'));
}
