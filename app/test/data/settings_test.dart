import 'package:app/core/db/database.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/data/models.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/settings_repository.dart';
import 'package:app/data/topics_repository.dart';
import 'package:drift/drift.dart' show driftRuntimeOptions;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

import 'test_db.dart';

void main() {
  late AppDatabase db;
  late AppSettingsRepository settings;

  // The migration test opens a second database on purpose; it is a different one.
  setUpAll(() => driftRuntimeOptions.dontWarnAboutMultipleDatabases = true);

  setUp(() {
    db = memoryDb();
    settings = AppSettingsRepository(db);
  });
  tearDown(() => db.close());

  group('notification choices', () {
    test('start with everything on and nothing paused', () async {
      final prefs = await settings.read();
      expect(
        (prefs.enabled, prefs.sound, prefs.showContent, prefs.closeToTray),
        (true, true, true, true),
      );
      expect(prefs.pausedUntil, isNull);
      expect(prefs.pausedAt(DateTime(2025)), isFalse);
    });

    test('are kept one by one', () async {
      await settings.setEnabled(false);
      await settings.setSound(false);
      await settings.setShowContent(false);
      await settings.setCloseToTray(false);

      final prefs = await settings.read();
      expect(
        (prefs.enabled, prefs.sound, prefs.showContent, prefs.closeToTray),
        (false, false, false, false),
      );
    });

    test('can be turned back on', () async {
      await settings.setSound(false);
      await settings.setSound(true);
      expect((await settings.read()).sound, isTrue);
    });

    test('pause until a time, then resume', () async {
      final until = DateTime(2025, 1, 15, 13);
      await settings.setPausedUntil(until);

      final prefs = await settings.read();
      expect(prefs.pausedUntil, until);
      expect(prefs.pausedAt(DateTime(2025, 1, 15, 12, 59)), isTrue);
      expect(prefs.pausedAt(until), isFalse, reason: 'the time itself is over');
      expect(prefs.pausedAt(DateTime(2025, 1, 15, 14)), isFalse);

      await settings.setPausedUntil(null);
      expect((await settings.read()).pausedUntil, isNull);
    });

    test('ignore a pause that cannot be read', () async {
      await db
          .into(db.appSettings)
          .insert(
            AppSettingsCompanion.insert(
              key: 'notifications.pausedUntil',
              value: 'soon',
            ),
          );
      expect((await settings.read()).pausedUntil, isNull);
    });

    test('are announced when they change', () async {
      final seen = <bool>[];
      final sub = settings.watch().listen((p) => seen.add(p.enabled));
      await pumpEventQueue();
      await settings.setEnabled(false);
      await pumpEventQueue();
      await settings.setEnabled(true);
      await pumpEventQueue();
      await sub.cancel();

      expect(seen, containsAllInOrder([true, false, true]));
    });

    test('compare equal when they hold the same choices', () {
      expect(const NotificationPrefs(), const NotificationPrefs());
      expect(
        const NotificationPrefs(sound: false),
        isNot(const NotificationPrefs()),
      );
      expect(
        const NotificationPrefs().hashCode,
        const NotificationPrefs().hashCode,
      );
    });
  });

  test(
    'a database from before settings existed is upgraded and keeps its data',
    () async {
      final old = AppDatabase(
        NativeDatabase.memory(
          setup: (raw) {
            raw.execute('''
CREATE TABLE servers (id INTEGER PRIMARY KEY AUTOINCREMENT, host TEXT NOT NULL UNIQUE,
  url TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)));
CREATE TABLE topics (id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE, name TEXT NOT NULL,
  muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0, 1)), min_priority INTEGER NOT NULL DEFAULT 1,
  cursor TEXT NULL, read_marker TEXT NULL,
  created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)), UNIQUE (server_id, name));
CREATE TABLE messages (server_id INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
  id TEXT NOT NULL, topic TEXT NOT NULL, time INTEGER NOT NULL, title TEXT NULL,
  body TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 3, tags TEXT NOT NULL DEFAULT '[]',
  click TEXT NULL, PRIMARY KEY (server_id, id));
CREATE INDEX messages_by_topic ON messages (server_id, topic, id);
INSERT INTO servers (host, url) VALUES ('example.com', 'https://example.com');
INSERT INTO topics (server_id, name, muted) VALUES (1, 'alerts', 1);
INSERT INTO messages (server_id, id, topic, time, body) VALUES (1, 'a1', 'alerts', 1700000000, 'kept');
PRAGMA user_version = 1;
''');
          },
        ),
      );
      addTearDown(old.close);

      // The new table is there and works...
      final upgraded = AppSettingsRepository(old);
      await upgraded.setSound(false);
      expect((await upgraded.read()).sound, isFalse);
      // ...and nothing that was there is lost.
      expect((await old.select(old.servers).getSingle()).host, 'example.com');
      expect((await old.select(old.topics).getSingle()).muted, isTrue);
      expect((await old.select(old.messages).getSingle()).body, 'kept');
    },
  );

  group('a topic\'s notification choices', () {
    late TopicsRepository topics;
    final address = ServerAddress.parse('https://example.com')!;

    setUp(() async {
      topics = TopicsRepository(db);
      await topics.subscribe(address, 'alerts');
    });

    test('are muted, and a minimum priority', () async {
      final before = (await topics.contextOf(1, 'alerts'))!;
      expect(
        (before.muted, before.minPriority, before.host),
        (false, 1, 'example.com'),
      );

      await topics.setMuted(before.topicId, true);
      await topics.setMinPriority(before.topicId, 4);

      final after = (await topics.contextOf(1, 'alerts'))!;
      expect((after.muted, after.minPriority), (true, 4));
    });

    test('keep a minimum priority between 1 and 5', () async {
      final id = (await topics.contextOf(1, 'alerts'))!.topicId;
      await topics.setMinPriority(id, 9);
      expect((await topics.contextOf(1, 'alerts'))!.minPriority, 5);
      await topics.setMinPriority(id, -3);
      expect((await topics.contextOf(1, 'alerts'))!.minPriority, 1);
    });

    test('are only for a topic the user is subscribed to', () async {
      expect(await topics.contextOf(1, 'nope'), isNull);
      expect(await topics.contextOf(99, 'alerts'), isNull);
    });

    test('are kept per topic', () async {
      await topics.subscribe(address, 'other');
      await topics.setMuted(
        (await topics.contextOf(1, 'alerts'))!.topicId,
        true,
      );
      expect((await topics.contextOf(1, 'other'))!.muted, isFalse);
    });
  });

  group('stored messages', () {
    late MessagesRepository messages;
    late List<IncomingMessage> events;

    setUp(() async {
      messages = MessagesRepository(db);
      events = [];
      messages.stored.listen(events.add);
      await TopicsRepository(db)
          .subscribe(ServerAddress.parse('https://example.com')!, 'alerts');
    });

    test('are announced once each, after they are stored', () async {
      await messages.store(incoming(1, 'alerts', 1, title: 'one'));
      await messages.store(incoming(1, 'alerts', 2, title: 'two'));
      await pumpEventQueue();

      expect(events.map((m) => m.title), ['one', 'two']);
      expect((await db.select(db.messages).get()), hasLength(2));
    });

    test('are not announced again when they arrive twice', () async {
      await messages.store(incoming(1, 'alerts', 1));
      await messages.store(incoming(1, 'alerts', 1));
      await pumpEventQueue();
      expect(events, hasLength(1));
    });

    test('are not announced for a topic the user left', () async {
      await messages.store(incoming(1, 'gone', 1));
      await pumpEventQueue();
      expect(events, isEmpty);
    });

    test('say whether they are the user\'s own', () async {
      await messages.store(
        const IncomingMessage(
          serverId: 1,
          id: 'own',
          topic: 'alerts',
          time: 1,
          mine: true,
        ),
      );
      await pumpEventQueue();
      expect(events.single.mine, isTrue);
      expect((await db.select(db.messages).getSingle()).mine, isTrue);
    });

    test(
      'are marked as the user\'s when the server\'s copy came first',
      () async {
        // The live connection delivers a message before the answer to sending it comes back.
        expect(
          await messages.store(
            const IncomingMessage(
              serverId: 1,
              id: 'm',
              topic: 'alerts',
              time: 1,
              body: 'hi',
            ),
          ),
          isTrue,
        );
        expect((await db.select(db.messages).getSingle()).mine, isFalse);

        expect(
          await messages.store(
            const IncomingMessage(
              serverId: 1,
              id: 'm',
              topic: 'alerts',
              time: 1,
              body: 'hi',
              mine: true,
            ),
          ),
          isFalse,
        );

        final rows = await db.select(db.messages).get();
        expect(rows, hasLength(1));
        expect(rows.single.mine, isTrue);
      },
    );

    test('are not unmarked when the server\'s copy arrives after', () async {
      await messages.store(
        const IncomingMessage(
          serverId: 1,
          id: 'm',
          topic: 'alerts',
          time: 1,
          mine: true,
        ),
      );
      await messages.store(
        const IncomingMessage(serverId: 1, id: 'm', topic: 'alerts', time: 1),
      );
      expect((await db.select(db.messages).getSingle()).mine, isTrue);
    });

    test('say whether they are history', () async {
      await messages.store(
        const IncomingMessage(
          serverId: 1,
          id: 'h',
          topic: 'alerts',
          time: 1,
          silent: true,
        ),
      );
      await messages.store(incoming(1, 'alerts', 5));
      await pumpEventQueue();
      expect(events.map((m) => m.silent), [true, false]);
    });
  });
}
