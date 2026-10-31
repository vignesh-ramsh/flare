import 'package:app/core/db/database.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/data/models.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/servers_repository.dart';
import 'package:app/data/topics_repository.dart';
import 'package:flutter_test/flutter_test.dart';

import 'test_db.dart';

void main() {
  late AppDatabase db;
  late TopicsRepository topics;
  late MessagesRepository messages;
  late ServersRepository servers;

  final example = ServerAddress.parse('https://example.com')!;
  final other = ServerAddress.parse('https://other.example:8443')!;

  setUp(() {
    db = memoryDb();
    topics = TopicsRepository(db);
    messages = MessagesRepository(db);
    servers = ServersRepository(db);
  });

  tearDown(() => db.close());

  Future<int> serverId(String host) async => (await (db.select(
    db.servers,
  )..where((s) => s.host.equals(host))).getSingle()).id;

  Future<TopicRow> topicRow(String name) =>
      (db.select(db.topics)..where((t) => t.name.equals(name))).getSingle();

  group('subscribe', () {
    test('adds the server and the topic', () async {
      expect(await topics.subscribe(example, 'alerts'), SubscribeResult.added);

      final all = await topics.watchSummaries().first;
      expect(all, hasLength(1));
      expect(all.single.host, 'example.com');
      expect(all.single.name, 'alerts');
      expect(all.single.unread, 0);
      expect(all.single.last, isNull);
    });

    test('says so when already subscribed, and adds nothing', () async {
      await topics.subscribe(example, 'alerts');
      expect(
        await topics.subscribe(example, 'alerts'),
        SubscribeResult.alreadySubscribed,
      );
      expect(await db.select(db.topics).get(), hasLength(1));
    });

    test('shares one server between topics, and keeps servers apart', () async {
      await topics.subscribe(example, 'a');
      await topics.subscribe(example, 'b');
      await topics.subscribe(other, 'a');

      expect(await db.select(db.servers).get(), hasLength(2));
      expect(await db.select(db.topics).get(), hasLength(3));
    });

    test('counts topics per server', () async {
      await topics.subscribe(example, 'a');
      await topics.subscribe(example, 'b');
      await topics.subscribe(other, 'a');

      final counts = await servers.watchWithCounts().first;
      expect(
        {for (final c in counts) c.server.host: c.topics},
        {'example.com': 2, 'other.example:8443': 1},
      );
    });

    test('lists servers with no topics too', () async {
      await db
          .into(db.servers)
          .insert(
            ServersCompanion.insert(
              host: 'empty.example',
              url: 'https://empty.example',
            ),
          );
      final counts = await servers.watchWithCounts().first;
      expect(counts.single.topics, 0);
    });
  });

  group('unsubscribe', () {
    test('removes the topic and its messages, and nothing else', () async {
      await topics.subscribe(example, 'alerts');
      await topics.subscribe(example, 'backups');
      await topics.subscribe(other, 'alerts');
      final a = await serverId('example.com');
      final o = await serverId('other.example:8443');
      await messages.store(incoming(a, 'alerts', 1));
      await messages.store(incoming(a, 'alerts', 2));
      await messages.store(incoming(a, 'backups', 3));
      await messages.store(incoming(o, 'alerts', 4));

      await topics.unsubscribe(a, 'alerts');

      final left = await topics.watchSummaries().first;
      expect(
        left.map((t) => '${t.host}/${t.name}'),
        unorderedEquals(['example.com/backups', 'other.example:8443/alerts']),
      );
      final kept = await db.select(db.messages).get();
      expect(
        kept.map((m) => (m.serverId, m.topic)),
        unorderedEquals([(a, 'backups'), (o, 'alerts')]),
      );
    });

    test(
      'keeps the server, so a sign-in is not lost with the last topic',
      () async {
        await topics.subscribe(example, 'alerts');
        final a = await serverId('example.com');

        await topics.unsubscribe(a, 'alerts');

        expect(await db.select(db.topics).get(), isEmpty);
        expect(await db.select(db.servers).get(), hasLength(1));
      },
    );

    test('can be subscribed to again, from nothing', () async {
      await topics.subscribe(example, 'alerts');
      final a = await serverId('example.com');
      await messages.store(incoming(a, 'alerts', 1));
      await topics.markRead(a, 'alerts');
      await topics.unsubscribe(a, 'alerts');

      expect(await topics.subscribe(example, 'alerts'), SubscribeResult.added);

      final again = (await topics.watchSummaries().first).single;
      expect(again.messageCount, 0);
      expect(again.unread, 0);
      expect(await topics.cursorOf(a, 'alerts'), isNull);
    });

    test('does nothing for a topic that is not there', () async {
      await topics.subscribe(example, 'alerts');
      final a = await serverId('example.com');
      await topics.unsubscribe(a, 'nope');
      await topics.unsubscribe(999, 'alerts');
      expect(await db.select(db.topics).get(), hasLength(1));
    });
  });

  group('store', () {
    late int id;
    setUp(() async {
      await topics.subscribe(example, 'alerts');
      id = await serverId('example.com');
    });

    test('keeps a message once, however often it arrives', () async {
      expect(await messages.store(incoming(id, 'alerts', 1)), isTrue);
      expect(await messages.store(incoming(id, 'alerts', 1)), isFalse);
      expect(await db.select(db.messages).get(), hasLength(1));
    });

    test('keeps every field, including tags', () async {
      await messages.store(
        incoming(
          id,
          'alerts',
          1,
          title: 'Disk',
          body: 'full',
          priority: 5,
          tags: ['db', 'prod'],
        ),
      );
      final row = await db.select(db.messages).getSingle();
      expect(
        (row.title, row.body, row.priority, row.time),
        ('Disk', 'full', 5, 1_700_000_001),
      );
      expect(row.tags, ['db', 'prod']);
    });

    test('moves the cursor forward, never back', () async {
      await messages.store(incoming(id, 'alerts', 5));
      expect((await topicRow('alerts')).cursor, mid(5));

      await messages.store(incoming(id, 'alerts', 3));
      expect((await topicRow('alerts')).cursor, mid(5));

      await messages.store(incoming(id, 'alerts', 9));
      expect((await topicRow('alerts')).cursor, mid(9));
    });

    test('drops messages for topics that are not subscribed', () async {
      expect(await messages.store(incoming(id, 'other', 1)), isFalse);
      expect(await db.select(db.messages).get(), isEmpty);
    });

    test('keeps the same ID on two servers apart', () async {
      await topics.subscribe(other, 'alerts');
      final otherId = await serverId('other.example:8443');

      expect(await messages.store(incoming(id, 'alerts', 1)), isTrue);
      expect(await messages.store(incoming(otherId, 'alerts', 1)), isTrue);
    });

    test('lists a topic newest first, and only that topic', () async {
      await topics.subscribe(example, 'other');
      await messages.store(incoming(id, 'alerts', 1));
      await messages.store(incoming(id, 'alerts', 3));
      await messages.store(incoming(id, 'alerts', 2));
      await messages.store(incoming(id, 'other', 4));

      final list = await messages.watchTopic(id, 'alerts').first;
      expect(list.map((m) => m.id), [mid(3), mid(2), mid(1)]);
    });

    test('the inbox mixes topics and servers, newest first', () async {
      await topics.subscribe(example, 'second');
      await topics.subscribe(other, 'alerts');
      final otherId = await serverId('other.example:8443');
      await messages.store(incoming(id, 'alerts', 1));
      await messages.store(incoming(otherId, 'alerts', 3));
      await messages.store(incoming(id, 'second', 2));

      final inbox = await messages.watchInbox().first;
      expect(inbox.map((i) => (i.message.id, i.host)), [
        (mid(3), 'other.example:8443'),
        (mid(2), 'example.com'),
        (mid(1), 'example.com'),
      ]);
    });
  });

  group('unread', () {
    late int id;
    setUp(() async {
      await topics.subscribe(example, 'alerts');
      id = await serverId('example.com');
    });

    Future<TopicSummary> summary() async =>
        (await topics.watchSummaries().first).single;

    test('every message is unread until the topic is read', () async {
      await messages.store(incoming(id, 'alerts', 1));
      await messages.store(incoming(id, 'alerts', 2));
      expect((await summary()).unread, 2);
      expect((await summary()).messageCount, 2);
    });

    test(
      'reading marks everything so far as read, and later messages unread',
      () async {
        await messages.store(incoming(id, 'alerts', 1));
        await messages.store(incoming(id, 'alerts', 2));
        await topics.markRead(id, 'alerts');
        expect((await summary()).unread, 0);

        await messages.store(incoming(id, 'alerts', 3));
        expect((await summary()).unread, 1);
      },
    );

    test('the marker never moves back', () async {
      await messages.store(incoming(id, 'alerts', 5));
      await topics.markRead(id, 'alerts');
      await messages.store(incoming(id, 'alerts', 2)); // late, older ID
      await topics.markRead(id, 'alerts');

      expect((await topicRow('alerts')).readMarker, mid(5));
    });

    test('marking an empty topic read does nothing', () async {
      await topics.markRead(id, 'alerts');
      expect((await topicRow('alerts')).readMarker, isNull);
    });

    test('one topic being read leaves another unread', () async {
      await topics.subscribe(example, 'other');
      await messages.store(incoming(id, 'alerts', 1));
      await messages.store(incoming(id, 'other', 2));
      await topics.markRead(id, 'alerts');

      final byName = {
        for (final t in await topics.watchSummaries().first) t.name: t.unread,
      };
      expect(byName, {'alerts': 0, 'other': 1});
    });
  });

  group('summaries', () {
    test(
      'show the newest message, and order topics by recent activity',
      () async {
        await topics.subscribe(example, 'quiet');
        await topics.subscribe(example, 'old');
        await topics.subscribe(example, 'fresh');
        final id = await serverId('example.com');
        await messages.store(incoming(id, 'old', 1, title: 'first'));
        await messages.store(
          incoming(id, 'fresh', 2, title: 'second', priority: 4),
        );
        await messages.store(
          incoming(id, 'fresh', 3, title: 'third', priority: 5),
        );

        final list = await topics.watchSummaries().first;
        expect(list.map((t) => t.name), ['fresh', 'old', 'quiet']);
        expect(list.first.last!.title, 'third');
        expect(list.first.last!.priority, 5);
        expect(
          list.first.last!.time,
          DateTime.fromMillisecondsSinceEpoch(1_700_000_003 * 1000),
        );
        expect(list.last.last, isNull);
      },
    );

    test('use the text as the preview when a message has no title', () async {
      await topics.subscribe(example, 'alerts');
      final id = await serverId('example.com');
      await messages.store(incoming(id, 'alerts', 1, body: 'just text'));
      expect(
        (await topics.watchSummaries().first).single.last!.preview,
        'just text',
      );
    });

    test('watchSummary finds one topic by host and name', () async {
      await topics.subscribe(example, 'alerts');
      await topics.subscribe(other, 'alerts');

      final one = await topics.watchSummary((
        host: 'other.example:8443',
        name: 'alerts',
      )).first;
      expect(one!.host, 'other.example:8443');
      expect(
        await topics.watchSummary((host: 'example.com', name: 'nope')).first,
        isNull,
      );
    });

    test('update when a message arrives', () async {
      await topics.subscribe(example, 'alerts');
      final id = await serverId('example.com');

      final seen = <int>[];
      final sub = topics.watchSummaries().listen(
        (l) => seen.add(l.single.unread),
      );
      await pumpEventQueue();
      await messages.store(incoming(id, 'alerts', 1));
      await pumpEventQueue();
      await sub.cancel();

      expect(seen, containsAllInOrder([0, 1]));
    });
  });

  group('prune', () {
    test('deletes old messages, keeps new ones and the cursor', () async {
      await topics.subscribe(example, 'alerts');
      final id = await serverId('example.com');
      await messages.store(incoming(id, 'alerts', 1, time: 1000));
      await messages.store(incoming(id, 'alerts', 2, time: 3000));

      final removed = await messages.pruneOlderThan(
        DateTime.fromMillisecondsSinceEpoch(2000 * 1000),
      );

      expect(removed, 1);
      expect((await db.select(db.messages).get()).map((m) => m.id), [mid(2)]);
      expect((await topicRow('alerts')).cursor, mid(2));
    });

    test('keeps 30 days by default', () {
      expect(defaultHistory, const Duration(days: 30));
    });
  });

  test('removing a server removes its topics and messages', () async {
    await topics.subscribe(example, 'alerts');
    final id = await serverId('example.com');
    await messages.store(incoming(id, 'alerts', 1));

    await (db.delete(db.servers)..where((s) => s.id.equals(id))).go();

    expect(await db.select(db.topics).get(), isEmpty);
    expect(await db.select(db.messages).get(), isEmpty);
  });
}
