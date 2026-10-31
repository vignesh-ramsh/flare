@Timeout(Duration(seconds: 90))
library;

import 'dart:io';

import 'package:app/auth/auth_service.dart';
import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/server_status.dart';
import 'package:drift/native.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../auth/memory_store.dart';
import '../net/fake_server.dart' show eventually;
import 'real_server.dart';

/// Signing in against the real Flare server, to prove the app's REST client, its sign-in
/// service and its connection agree with it. Skipped unless FLARE_SERVER_BIN is set.
void main() {
  group('against the real server', () {
    RealServer? server;
    ServerApi? api;
    ProviderContainer? container;

    tearDown(() async {
      container?.dispose();
      api?.close();
      await server?.dispose();
      container = null;
      api = null;
      server = null;
    });

    Future<RealServer> closedServer() =>
        RealServer.start(env: {'FLARE_AUTH_DEFAULT_ACCESS': 'deny-all'});

    group('config', () {
      test('reads the name and the access mode of an open server', () async {
        server = await RealServer.start();
        api = ServerApi();
        final info = await api!.config(server!.url);

        expect(info.name, isNotEmpty);
        expect(info.apiVersion, 1);
        expect(info.defaultAccess, 'read-write');
        expect(info.needsSignIn, isFalse);
        expect(info.features, contains('websocket'));
      });

      test('says a closed server needs a sign-in', () async {
        server = await closedServer();
        api = ServerApi();
        final info = await api!.config(server!.url);

        expect(info.defaultAccess, 'deny-all');
        expect(info.needsSignIn, isTrue);
      });

      test('says an address that is not a Flare server is not one', () async {
        final other = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        addTearDown(() => other.close(force: true));
        other.listen((request) {
          request.response
            ..statusCode = 200
            ..write('<html>hello</html>')
            ..close();
        });
        api = ServerApi();

        await expectLater(
          api!.config(Uri.parse('http://127.0.0.1:${other.port}')),
          throwsA(
            isA<ApiException>().having(
              (e) => e.failure,
              'failure',
              ApiFailure.notSupported,
            ),
          ),
        );
      });

      test('says an address nobody answers on is unreachable', () async {
        final probe = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
        final port = probe.port;
        await probe.close();
        api = ServerApi();

        await expectLater(
          api!.config(Uri.parse('http://127.0.0.1:$port')),
          throwsA(
            isA<ApiException>().having(
              (e) => e.failure,
              'failure',
              ApiFailure.unreachable,
            ),
          ),
        );
      });
    });

    group('login', () {
      test('returns a token that works', () async {
        server = await closedServer();
        await server!.addUser('alice');
        api = ServerApi();

        final result = await api!.login(
          server!.url,
          user: 'alice',
          password: RealServer.testPassword,
          label: 'Test',
        );

        expect(result.user, 'alice');
        expect(result.token, startsWith('flr_'));
        expect(result.tokenId, isNotEmpty);
        // Admin-less user on a closed server: the token is accepted, so a topic that needs
        // a login answers differently than it does for a stranger (checked below).
        await expectLater(
          server!.publish('private', 'x', token: 'flr_${'x' * 43}'),
          throwsStateError,
        );
      });

      test(
        'refuses a wrong password and an unknown user the same way',
        () async {
          server = await closedServer();
          await server!.addUser('alice');
          api = ServerApi();

          for (final (user, password) in [
            ('alice', 'wrong'),
            ('nobody', RealServer.testPassword),
          ]) {
            await expectLater(
              api!.login(server!.url, user: user, password: password),
              throwsA(
                isA<ApiException>()
                    .having(
                      (e) => e.failure,
                      'failure',
                      ApiFailure.badCredentials,
                    )
                    .having((e) => e.code, 'code', 40102),
              ),
              reason: user,
            );
          }
        },
      );

      test('slows down after too many wrong passwords', () async {
        server = await closedServer();
        await server!.addUser('alice');
        api = ServerApi();

        ApiFailure? last;
        for (var i = 0; i < 15 && last != ApiFailure.throttled; i++) {
          try {
            await api!.login(server!.url, user: 'alice', password: 'wrong$i');
          } on ApiException catch (e) {
            last = e.failure;
          }
        }
        expect(last, ApiFailure.throttled);
      });
    });

    group('revoking a token', () {
      test('makes the token stop working', () async {
        server = await closedServer();
        await server!.addUser('alice', admin: true);
        api = ServerApi();
        final login = await api!.login(
          server!.url,
          user: 'alice',
          password: RealServer.testPassword,
        );
        await server!.publish('private', 'works', token: login.token);

        await api!.revokeToken(
          server!.url,
          token: login.token,
          tokenId: login.tokenId,
        );

        await expectLater(
          server!.publish('private', 'refused', token: login.token),
          throwsStateError,
        );
      });

      test('treats a token that is already gone as done', () async {
        server = await closedServer();
        await server!.addUser('alice', admin: true);
        api = ServerApi();
        final login = await api!.login(
          server!.url,
          user: 'alice',
          password: RealServer.testPassword,
        );
        await api!.revokeToken(
          server!.url,
          token: login.token,
          tokenId: login.tokenId,
        );

        await api!.revokeToken(
          server!.url,
          token: login.token,
          tokenId: login.tokenId,
        );
      });
    });

    group('the app\'s own sign-in and connection', () {
      late AppDatabase db;
      late MemorySecretStore secrets;

      Future<ServerRow> setUpApp(RealServer s, List<String> topics) async {
        db = AppDatabase(NativeDatabase.memory());
        secrets = MemorySecretStore();
        container = ProviderContainer(
          overrides: [
            databaseProvider.overrideWithValue(db),
            secretStoreProvider.overrideWithValue(secrets),
          ],
        );
        final address = ServerAddress.parse(s.url.toString())!;
        for (final topic in topics) {
          await container!
              .read(topicsRepositoryProvider)
              .subscribe(address, topic);
        }
        return db.select(db.servers).getSingle();
      }

      Future<void> eventuallyTopic(
        ServerRow row,
        String topic,
        bool Function(TopicLink?) test,
      ) => eventually(() {
        final status = container!
            .read(connectionManagerProvider)
            .current[row.id];
        return test(status?.topics[topic]);
      }, reason: 'topic $topic');

      tearDown(() async => db.close());

      test('signing in opens the topics the server kept closed, and signing out closes them again', () async {
        server = await closedServer();
        await server!.addUser('alice', admin: true);
        final row = await setUpApp(server!, ['private']);

        // Anonymous: the server says to sign in.
        container!.read(connectionManagerProvider).start();
        await eventuallyTopic(row, 'private', (t) => t?.errorCode == 40101);

        // Sign in: the same connection logic reconnects with the token, and the topic is live.
        final credentials = await container!
            .read(authServiceProvider)
            .signIn(row, user: 'alice', password: RealServer.testPassword);
        expect(credentials.user, 'alice');
        await eventuallyTopic(row, 'private', (t) => t?.live ?? false);
        await server!.publish('private', 'for alice', token: credentials.token);
        await eventually(
          () async => (await db.select(db.messages).get()).any(
            (m) => m.body == 'for alice',
          ),
        );

        // Sign out: the token is revoked on the server and the topic is closed again.
        final revoked = await container!.read(authServiceProvider).signOut(row);
        expect(revoked, isTrue);
        expect(secrets.values, isEmpty);
        await eventuallyTopic(row, 'private', (t) => t?.errorCode == 40101);
        await expectLater(
          server!.publish('private', 'x', token: credentials.token),
          throwsStateError,
        );
      });

      test('a wrong password signs nothing in', () async {
        server = await closedServer();
        await server!.addUser('alice');
        final row = await setUpApp(server!, ['private']);

        await expectLater(
          container!
              .read(authServiceProvider)
              .signIn(row, user: 'alice', password: 'wrong'),
          throwsA(isA<ApiException>()),
        );
        expect(container!.read(credentialsProvider).byServer, isEmpty);
        expect(secrets.values, isEmpty);
      });

      test('a saved token the server no longer accepts is reported, not retried forever', () async {
        server = await closedServer();
        await server!.addUser('alice', admin: true);
        final row = await setUpApp(server!, ['private']);
        await container!
            .read(credentialsProvider.notifier)
            .save(
              row.url,
              const Credentials(
                token: 'flr_wrongwrongwrongwrongwrongwrongwrongwrongxxx',
                tokenId: 'x',
                user: 'alice',
              ),
            );

        container!.read(connectionManagerProvider).start();
        await eventually(
          () =>
              container!
                  .read(connectionManagerProvider)
                  .current[row.id]
                  ?.state ==
              LinkState.authFailed,
          reason: 'auth failed',
        );
      });
    });
  }, skip: serverBinary == null ? skipWithoutServer : false);
}
