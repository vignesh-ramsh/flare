import 'dart:convert';

import 'package:app/auth/auth_service.dart';
import 'package:app/auth/credentials.dart';
import 'package:app/brand/brand.g.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../data/test_db.dart';
import '../net/spy_manager.dart';
import 'memory_store.dart';

void main() {
  late AppDatabase db;
  late MemorySecretStore store;
  late SpyManager spy;
  late List<http.Request> requests;
  late http.Response Function(http.Request) answer;
  late ProviderContainer container;

  final server = ServerRow(
    id: 1,
    host: 'example.com',
    url: 'https://example.com',
    createdAt: DateTime(2025),
  );

  http.Response login200() => http.Response(
    jsonEncode({
      'id': 'tok_1',
      'created': 1,
      'token': 'flr_secret',
      'user': 'alice',
      'role': 'user',
    }),
    200,
  );

  setUp(() {
    db = memoryDb();
    store = MemorySecretStore();
    spy = SpyManager(TopicsRepository(db));
    requests = [];
    answer = (_) => login200();
    container = ProviderContainer(
      overrides: [
        secretStoreProvider.overrideWithValue(store),
        connectionManagerProvider.overrideWithValue(spy),
        apiProvider.overrideWithValue(
          ServerApi(
            client: MockClient((request) async {
              requests.add(request);
              return answer(request);
            }),
          ),
        ),
      ],
    );
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  AuthService service() => container.read(authServiceProvider);

  group('signIn', () {
    test('saves the token, and reconnects the server with it', () async {
      final credentials = await service().signIn(
        server,
        user: 'alice',
        password: 'hunter2',
      );

      expect(
        (credentials.token, credentials.tokenId, credentials.user),
        ('flr_secret', 'tok_1', 'alice'),
      );
      expect(
        container
            .read(credentialsProvider.notifier)
            .of('https://example.com')!
            .token,
        'flr_secret',
      );
      expect(store.values.keys, ['credentials:https://example.com']);
      expect(spy.loginChanges, ['https://example.com']);
    });

    test('never keeps the password', () async {
      await service().signIn(server, user: 'alice', password: 'hunter2-secret');

      expect(
        store.values.values.any((v) => v.contains('hunter2-secret')),
        isFalse,
      );
      expect(
        container
            .read(credentialsProvider)
            .byServer
            .values
            .any((c) => c.token.contains('hunter2')),
        isFalse,
      );
    });

    test(
      'names the token after the app and the platform, for the list of devices',
      () async {
        await service().signIn(server, user: 'alice', password: 'x');

        final label =
            (jsonDecode(requests.single.body) as Map<String, dynamic>)['label']
                as String;
        expect(label, startsWith('${Brand.name} ('));
      },
    );

    test(
      'saves nothing and reconnects nothing when the sign-in is refused',
      () async {
        answer = (_) => http.Response(
          jsonEncode({'code': 40102, 'http': 401, 'error': 'no'}),
          401,
        );

        await expectLater(
          service().signIn(server, user: 'alice', password: 'wrong'),
          throwsA(
            isA<ApiException>().having(
              (e) => e.failure,
              'failure',
              ApiFailure.badCredentials,
            ),
          ),
        );
        expect(store.values, isEmpty);
        expect(spy.loginChanges, isEmpty);
      },
    );

    test('keeps the old sign-in when a new attempt is refused', () async {
      await service().signIn(server, user: 'alice', password: 'x');
      answer = (_) => http.Response(
        jsonEncode({'code': 40102, 'http': 401, 'error': 'no'}),
        401,
      );
      await expectLater(
        service().signIn(server, user: 'bob', password: 'bad'),
        throwsA(isA<ApiException>()),
      );

      expect(
        container
            .read(credentialsProvider.notifier)
            .of('https://example.com')!
            .user,
        'alice',
      );
    });
  });

  group('signOut', () {
    setUp(() async {
      await service().signIn(server, user: 'alice', password: 'x');
      requests.clear();
      spy.loginChanges.clear();
    });

    test('revokes the token, forgets it, and reconnects without it', () async {
      answer = (_) => http.Response('', 204);
      final revoked = await service().signOut(server);

      expect(revoked, isTrue);
      expect(requests.single.method, 'DELETE');
      expect(requests.single.url.path, '/v1/account/tokens/tok_1');
      expect(requests.single.headers['Authorization'], 'Bearer flr_secret');
      expect(
        container.read(credentialsProvider.notifier).of('https://example.com'),
        isNull,
      );
      expect(store.values, isEmpty);
      expect(spy.loginChanges, ['https://example.com']);
    });

    test('signs out here even when the server cannot be reached, and says it could not revoke', () async {
      final api = ServerApi(
        client: MockClient((_) => throw http.ClientException('offline')),
      );
      final offline = ProviderContainer(
        overrides: [
          secretStoreProvider.overrideWithValue(store),
          connectionManagerProvider.overrideWithValue(spy),
          apiProvider.overrideWithValue(api),
        ],
      );
      addTearDown(offline.dispose);
      await offline.read(credentialsProvider.notifier).load();

      final revoked = await offline.read(authServiceProvider).signOut(server);

      expect(revoked, isFalse);
      expect(
        offline.read(credentialsProvider.notifier).of('https://example.com'),
        isNull,
      );
      expect(store.values, isEmpty);
      expect(spy.loginChanges, ['https://example.com']);
    });

    test('does nothing when not signed in', () async {
      await service().signOut(server);
      requests.clear();
      spy.loginChanges.clear();

      expect(await service().signOut(server), isTrue);
      expect(requests, isEmpty);
      expect(spy.loginChanges, isEmpty);
    });
  });
}
