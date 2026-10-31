@Timeout(Duration(seconds: 90))
library;

import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/publisher.dart';
import 'package:drift/native.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../auth/memory_store.dart';
import '../net/fake_server.dart' show eventually;
import 'real_server.dart';

/// Sending from the app to the real Flare server: the REST publish, what the server answers
/// to someone who may not send, and the copy that comes back on the live connection.
/// Skipped unless FLARE_SERVER_BIN is set.
void main() {
  group('publishing to the real server', () {
    RealServer? server;
    ProviderContainer? container;
    AppDatabase? db;

    tearDown(() async {
      container?.dispose();
      await db?.close();
      await server?.dispose();
      container = null;
      db = null;
      server = null;
    });

    Future<ServerRow> setUpApp(RealServer s, List<String> topics) async {
      db = AppDatabase(NativeDatabase.memory());
      container = ProviderContainer(
        overrides: [
          databaseProvider.overrideWithValue(db!),
          secretStoreProvider.overrideWithValue(MemorySecretStore()),
        ],
      );
      final address = ServerAddress.parse(s.url.toString())!;
      for (final topic in topics) {
        await container!
            .read(topicsRepositoryProvider)
            .subscribe(address, topic);
      }
      return db!.select(db!.servers).getSingle();
    }

    Future<List<MessageRow>> stored() => db!.select(db!.messages).get();

    Future<void> live(ServerRow row, String topic) => eventually(
      () =>
          container!
              .read(connectionManagerProvider)
              .current[row.id]
              ?.topics[topic]
              ?.live ??
          false,
      reason: 'topic $topic live',
    );

    Future<void> send(
      ServerRow row,
      String topic,
      String text, {
      String? title,
      int? priority,
    }) => container!
        .read(publisherProvider)
        .send(
          serverId: row.id,
          topic: topic,
          text: text,
          title: title,
          priority: priority,
        );

    Matcher failsWith(ApiFailure failure, [int? code]) {
      var matcher = isA<ApiException>().having(
        (e) => e.failure,
        'failure',
        failure,
      );
      if (code != null) matcher = matcher.having((e) => e.code, 'code', code);
      return throwsA(matcher);
    }

    Future<void> signedInAs(ServerRow row, String user, String token) =>
        container!
            .read(credentialsProvider.notifier)
            .save(row.url, Credentials(token: token, tokenId: 'x', user: user));

    test('an open server takes the message, and it is kept once, as the user\'s own', () async {
      server = await RealServer.start();
      final row = await setUpApp(server!, ['alerts']);
      container!.read(connectionManagerProvider).start();
      await live(row, 'alerts');

      await send(row, 'alerts', 'on my way', title: 'Status', priority: 4);

      // The live connection delivers the server's copy as well, in either order. Wait for
      // both to have happened, then there is still one message and it is the user's.
      await eventually(() async => (await stored()).isNotEmpty);
      await Future<void>.delayed(const Duration(milliseconds: 600));
      final rows = await stored();
      expect(rows, hasLength(1));
      expect(rows.single.body, 'on my way');
      expect(rows.single.title, 'Status');
      expect(rows.single.priority, 4);
      expect(rows.single.topic, 'alerts');
      expect(rows.single.mine, isTrue);
    });

    test('a message from someone else is not the user\'s own', () async {
      server = await RealServer.start();
      final row = await setUpApp(server!, ['alerts']);
      container!.read(connectionManagerProvider).start();
      await live(row, 'alerts');

      await send(row, 'alerts', 'mine');
      await server!.publish('alerts', 'theirs');

      await eventually(() async => (await stored()).length == 2);
      final byBody = {for (final m in await stored()) m.body: m.mine};
      expect(byBody, {'mine': true, 'theirs': false});
    });

    test(
      'a message the server holds for an offline topic still comes back marked',
      () async {
        server = await RealServer.start();
        final row = await setUpApp(server!, ['alerts']);
        // Not connected: the REST answer is the only copy the app sees.
        await send(row, 'alerts', 'sent offline-style');
        expect((await stored()).single.mine, isTrue);

        // Connecting afterwards replays the same message from the server: still one, still mine.
        container!.read(connectionManagerProvider).start();
        await live(row, 'alerts');
        await Future<void>.delayed(const Duration(milliseconds: 600));
        final rows = await stored();
        expect(rows, hasLength(1));
        expect(rows.single.mine, isTrue);
      },
    );

    group('on a server that keeps its topics closed', () {
      Future<RealServer> closed() =>
          RealServer.start(env: {'FLARE_AUTH_DEFAULT_ACCESS': 'deny-all'});

      test('says to sign in when the user has not', () async {
        server = await closed();
        final row = await setUpApp(server!, ['private']);

        await expectLater(
          send(row, 'private', 'hi'),
          failsWith(ApiFailure.needsSignIn, 40101),
        );
        expect(await stored(), isEmpty);
      });

      test('takes the message once the user has signed in', () async {
        server = await closed();
        final token = await server!.addUserWithToken('alice', admin: true);
        final row = await setUpApp(server!, ['private']);
        await signedInAs(row, 'alice', token);

        await send(row, 'private', 'from alice');

        final rows = await stored();
        expect(rows.single.body, 'from alice');
        expect(rows.single.mine, isTrue);
      });

      test('says no to a user who may not write to the topic', () async {
        server = await closed();
        final token = await server!.addUserWithToken('bob');
        final row = await setUpApp(server!, ['private']);
        await signedInAs(row, 'bob', token);

        await expectLater(
          send(row, 'private', 'hi'),
          failsWith(ApiFailure.forbidden, 40301),
        );
        expect(await stored(), isEmpty);
      });

      test(
        'says to sign in again when the saved token has been revoked',
        () async {
          server = await closed();
          final row = await setUpApp(server!, ['private']);
          await signedInAs(row, 'alice', 'flr_${'w' * 43}');

          await expectLater(
            send(row, 'private', 'hi'),
            failsWith(ApiFailure.badCredentials),
          );
        },
      );
    });

    test('says the message is too long, and keeps nothing', () async {
      server = await RealServer.start();
      final row = await setUpApp(server!, ['alerts']);

      await expectLater(
        send(row, 'alerts', 'x' * 5000),
        failsWith(ApiFailure.tooLarge, 41301),
      );
      expect(await stored(), isEmpty);
    });

    test('says the server cannot be reached when it has stopped', () async {
      server = await RealServer.start();
      final row = await setUpApp(server!, ['alerts']);
      await server!.stop();

      await expectLater(
        send(row, 'alerts', 'hi'),
        failsWith(ApiFailure.unreachable),
      );
      expect(await stored(), isEmpty);
    });
  }, skip: serverBinary == null ? skipWithoutServer : false);
}
