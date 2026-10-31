@Timeout(Duration(seconds: 60))
library;

import 'package:app/core/db/database.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/net/backoff.dart';
import 'package:app/net/connection_manager.dart';
import 'package:app/net/server_connection.dart';
import 'package:app/net/server_status.dart';
import 'package:drift/drift.dart' show OrderingTerm;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

import '../net/fake_server.dart' show eventually;
import 'real_server.dart';

/// The app's connection code against the real Flare server, to prove they agree on the
/// protocol. Skipped unless FLARE_SERVER_BIN points at the `flare` binary.
void main() {
  group('against the real server', () {
    late AppDatabase db;
    late TopicsRepository topics;
    late MessagesRepository messages;
    RealServer? server;
    ConnectionManager? manager;
    ServerConnection? connection;

    setUp(() {
      db = AppDatabase(NativeDatabase.memory());
      topics = TopicsRepository(db);
      messages = MessagesRepository(db);
    });

    tearDown(() async {
      await manager?.close();
      await connection?.close();
      await server?.dispose();
      await db.close();
      manager = null;
      connection = null;
      server = null;
    });

    ServerConnection connectTo(RealServer s, int serverId, {String? token}) {
      return ServerConnection(
        serverId: serverId,
        url: s.url,
        store: RepositoryConnectionStore(topics, messages),
        token: token == null ? null : () async => token,
        backoff: Backoff(
          base: const Duration(milliseconds: 50),
          max: const Duration(milliseconds: 400),
        ),
        connectTimeout: const Duration(seconds: 5),
      );
    }

    Future<int> subscribe(RealServer s, List<String> names) async {
      final address = ServerAddress.parse(s.url.toString())!;
      for (final name in names) {
        await topics.subscribe(address, name);
      }
      return (await db.select(db.servers).getSingle()).id;
    }

    Future<List<MessageRow>> stored(String topic) =>
        (db.select(db.messages)
              ..where((m) => m.topic.equals(topic))
              ..orderBy([(m) => OrderingTerm.asc(m.id)]))
            .get();

    test(
      'stores a message published with a plain HTTP request, with its details',
      () async {
        server = await RealServer.start();
        await subscribe(server!, ['alerts']);
        manager = ConnectionManager(
          topics: topics,
          factory: (row) => connectTo(server!, row.id),
        )..start();
        await eventually(
          () => manager!.current.values.any(
            (s) => s.topics['alerts']?.live ?? false,
          ),
        );

        final id = await server!.publish(
          'alerts',
          'db-01 /var is at 92%',
          title: 'Disk almost full',
          priority: 4,
          tags: ['db', 'prod'],
        );
        await eventually(() async => (await stored('alerts')).isNotEmpty);

        final row = (await stored('alerts')).single;
        expect(row.id, id);
        expect(row.title, 'Disk almost full');
        expect(row.body, 'db-01 /var is at 92%');
        expect(row.priority, 4);
        expect(row.tags, ['db', 'prod']);
        expect(
          row.time,
          closeTo(DateTime.now().millisecondsSinceEpoch ~/ 1000, 10),
        );
      },
    );

    test('a message with no title or priority gets the defaults', () async {
      server = await RealServer.start();
      final serverId = await subscribe(server!, ['plain']);
      connection = connectTo(server!, serverId)
        ..setTopics({'plain'})
        ..start();
      await eventually(() => connection!.status.topics['plain']?.live ?? false);

      await server!.publish('plain', 'just text');
      await eventually(() async => (await stored('plain')).isNotEmpty);

      final row = (await stored('plain')).single;
      expect((row.title, row.priority, row.body), (null, 3, 'just text'));
      expect(row.tags, isEmpty);
    });

    test(
      'a new subscription gets the messages the server already has, once',
      () async {
        server = await RealServer.start();
        final ids = [
          for (var i = 1; i <= 3; i++) await server!.publish('news', 'item $i'),
        ];
        final serverId = await subscribe(server!, ['news']);

        connection = connectTo(server!, serverId)
          ..setTopics({'news'})
          ..start();
        await eventually(() async => (await stored('news')).length == 3);

        expect((await stored('news')).map((m) => m.id), ids);
        expect(await topics.cursorOf(serverId, 'news'), ids.last);
      },
    );

    test(
      'catches up after the server restarts, with nothing missed or repeated',
      () async {
        server = await RealServer.start();
        final serverId = await subscribe(server!, ['jobs']);
        connection = connectTo(server!, serverId)
          ..setTopics({'jobs'})
          ..start();
        await eventually(
          () => connection!.status.topics['jobs']?.live ?? false,
        );

        final first = [
          await server!.publish('jobs', 'one'),
          await server!.publish('jobs', 'two'),
        ];
        await eventually(() async => (await stored('jobs')).length == 2);

        await server!.stop();
        await eventually(
          () => connection!.status.state == LinkState.waiting,
          reason: 'noticed the server going away',
        );
        await server!.restart();
        await eventually(
          () => connection!.status.topics['jobs']?.live ?? false,
        );

        final second = [
          await server!.publish('jobs', 'three'),
          await server!.publish('jobs', 'four'),
        ];
        await eventually(() async => (await stored('jobs')).length == 4);

        expect((await stored('jobs')).map((m) => m.id), [...first, ...second]);
        expect((await stored('jobs')).map((m) => m.body), [
          'one',
          'two',
          'three',
          'four',
        ]);
      },
    );

    test(
      'a restarted app resumes from its cursor and fetches only what is new',
      () async {
        server = await RealServer.start();
        final serverId = await subscribe(server!, ['jobs']);
        connection = connectTo(server!, serverId)
          ..setTopics({'jobs'})
          ..start();
        await server!.publish('jobs', 'one');
        await eventually(() async => (await stored('jobs')).length == 1);

        await connection!.close(); // the app is closed
        final missed = await server!.publish(
          'jobs',
          'two',
        ); // sent while the app was closed

        connection = connectTo(server!, serverId)
          ..setTopics({'jobs'})
          ..start();
        await eventually(() async => (await stored('jobs')).length == 2);

        expect((await stored('jobs')).last.id, missed);
        expect((await stored('jobs')).map((m) => m.body), ['one', 'two']);
      },
    );

    group('on a server that needs sign-in', () {
      Future<RealServer> closedServer() =>
          RealServer.start(env: {'FLARE_AUTH_DEFAULT_ACCESS': 'deny-all'});

      test(
        'an anonymous app is told to sign in, and stays connected',
        () async {
          server = await closedServer();
          final serverId = await subscribe(server!, ['private']);
          connection = connectTo(server!, serverId)
            ..setTopics({'private'})
            ..start();

          await eventually(
            () => connection!.status.topics['private']?.errorCode == 40101,
          );
          expect(connection!.status.state, LinkState.connected);
        },
      );

      test('a user with a token reads the topic', () async {
        server = await closedServer();
        final token = await server!.addUserWithToken('alice', admin: true);
        final serverId = await subscribe(server!, ['private']);
        connection = connectTo(server!, serverId, token: token)
          ..setTopics({'private'})
          ..start();
        await eventually(
          () => connection!.status.topics['private']?.live ?? false,
        );

        await server!.publish('private', 'secret', token: token);
        await eventually(() async => (await stored('private')).length == 1);
      });

      test(
        'a user with no access to the topic is told it is forbidden',
        () async {
          server = await closedServer();
          final token = await server!.addUserWithToken('bob');
          final serverId = await subscribe(server!, ['private']);
          connection = connectTo(server!, serverId, token: token)
            ..setTopics({'private'})
            ..start();

          await eventually(
            () => connection!.status.topics['private']?.errorCode == 40301,
          );
          expect(connection!.status.state, LinkState.connected);
        },
      );

      test('a token the server does not know stops the retrying', () async {
        server = await closedServer();
        final serverId = await subscribe(server!, ['private']);
        connection = connectTo(server!, serverId, token: 'flr_${'x' * 43}')
          ..setTopics({'private'})
          ..start();

        await eventually(
          () => connection!.status.state == LinkState.authFailed,
        );
      });
    });
  }, skip: serverBinary == null ? skipWithoutServer : false);
}
