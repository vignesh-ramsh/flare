import 'package:app/app/app.dart';
import 'package:app/app/router.dart';
import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/topics_repository.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';

import 'auth/memory_store.dart';
import 'data/test_db.dart';

/// The clock every widget test runs on: Wednesday 15 January 2025, noon.
final testNow = DateTime(2025, 1, 15, 12);

int _seconds(DateTime t) => t.millisecondsSinceEpoch ~/ 1000;

/// A database with the data the layout tests look at:
///
/// * `alerts-db` on example.com: 3 unread messages, the newest "Disk almost full" (high);
/// * `backups` on example.com: 1 message, already read;
/// * `deploys` on example.com: muted, 1 unread message from yesterday;
/// * `quiet` on example.com: no messages.
Future<AppDatabase> seededDb() async {
  final db = AppDatabase(NativeDatabase.memory());
  final topics = TopicsRepository(db);
  final messages = MessagesRepository(db);
  final server = ServerAddress.parse('https://example.com')!;
  for (final name in ['alerts-db', 'backups', 'deploys', 'quiet']) {
    await topics.subscribe(server, name);
  }
  final id = (await db.select(db.servers).getSingle()).id;

  // IDs sort like times do: deploys is oldest, then alerts-db and backups interleave.
  await messages.store(
    incoming(
      id,
      'deploys',
      1,
      title: 'api v2.14.0',
      body: 'Rolled out to production',
      time: _seconds(DateTime(2025, 1, 14, 17, 30)),
    ),
  );
  await messages.store(
    incoming(
      id,
      'alerts-db',
      2,
      title: 'Disk usage',
      body: 'db-01 /var is at 70%',
      time: _seconds(DateTime(2025, 1, 15, 7)),
    ),
  );
  await messages.store(
    incoming(
      id,
      'backups',
      3,
      title: 'Nightly backup finished',
      body: '2.1 GB in 4 minutes',
      time: _seconds(DateTime(2025, 1, 15, 9)),
    ),
  );
  await topics.markRead(id, 'backups');
  await messages.store(
    incoming(
      id,
      'alerts-db',
      4,
      title: 'Disk usage',
      body: 'db-01 /var is at 85%',
      priority: 2,
      tags: ['db'],
      time: _seconds(DateTime(2025, 1, 15, 9, 30)),
    ),
  );
  await messages.store(
    incoming(
      id,
      'alerts-db',
      5,
      title: 'Disk almost full',
      body: 'db-01 /var is at 92% (18 GB free).',
      priority: 4,
      tags: ['db', 'prod'],
      time: _seconds(DateTime(2025, 1, 15, 11, 6)),
    ),
  );
  await (db.update(db.topics)..where((t) => t.name.equals('deploys'))).write(
    const TopicsCompanion(muted: Value(true)),
  );
  return db;
}

/// Runs the whole app on [db] in a window [width] by [height] logical pixels.
Future<void> pumpApp(
  WidgetTester tester,
  AppDatabase db, {
  required double width,
  double height = 800,
  String location = '/',
  double textScale = 1,
  Brightness brightness = Brightness.light,
  List<Override> overrides = const [],

  /// Where sign-ins are kept; a fresh in-memory store if not given.
  SecretStore? secrets,

  /// Sign-ins already saved, by server address.
  Map<String, Credentials> signedIn = const {},

  /// Use the app's own router, with the first-run pages, instead of one that starts at [location].
  bool useAppRouter = false,

  /// A router the test made itself, instead of one that starts at [location].
  GoRouter? router,
}) async {
  tester.view.physicalSize = Size(width, height);
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = textScale;
  tester.platformDispatcher.platformBrightnessTestValue = brightness;
  addTearDown(() {
    tester.view.reset();
    tester.platformDispatcher.clearAllTestValues();
  });

  router ??= createRouter(initialLocation: location);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        databaseProvider.overrideWithValue(db),
        clockProvider.overrideWithValue(() => testNow),
        secretStoreProvider.overrideWithValue(secrets ?? MemorySecretStore()),
        if (signedIn.isNotEmpty)
          credentialsProvider.overrideWith(() => _Preloaded(signedIn)),
        if (!useAppRouter) routerProvider.overrideWithValue(router),
        ...overrides,
      ],
      child: const App(),
    ),
  );
  await settle(tester);

  // The app's own router starts at '/'; go where the test asked.
  if (useAppRouter && location != '/') {
    GoRouter.of(tester.element(find.byType(Scaffold).first)).go(location);
    await settle(tester);
  }
}

/// Lets database streams deliver and animations finish.
Future<void> settle(WidgetTester tester) async {
  await tester.runAsync(
    () => Future<void>.delayed(const Duration(milliseconds: 20)),
  );
  await tester.pumpAndSettle();
}

/// Closes the app and the database without leaving timers behind.
Future<void> shutDown(WidgetTester tester, AppDatabase db) async {
  await tester.pumpWidget(const SizedBox.shrink());
  // Drift cancels its streams with zero-length timers; let them fire before closing.
  await tester.pump(const Duration(seconds: 1));
  await tester.runAsync(db.close);
  await tester.pump(const Duration(seconds: 1));
}

/// A widget test on a database: seeded with [seededDb], or empty. The app and the
/// database are closed before the test ends, so no timers are left behind.
void testApp(
  String description,
  Future<void> Function(WidgetTester tester, AppDatabase db) body, {
  bool seeded = true,
}) {
  testWidgets(description, (tester) async {
    final db = (await tester.runAsync(
      () async =>
          seeded ? await seededDb() : AppDatabase(NativeDatabase.memory()),
    ))!;
    try {
      await body(tester, db);
    } finally {
      await shutDown(tester, db);
    }
  });
}

/// A credentials controller that starts with sign-ins already loaded, as after launch.
class _Preloaded extends CredentialsController {
  _Preloaded(this._initial);

  final Map<String, Credentials> _initial;

  @override
  CredentialsState build() => CredentialsState(byServer: _initial);
}
