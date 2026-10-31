import 'dart:convert';

import 'package:app/auth/credentials.dart';
import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/publisher.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../auth/memory_store.dart';
import '../data/test_db.dart';
import 'spy_manager.dart';

void main() {
  late AppDatabase db;
  late ProviderContainer container;
  late List<http.Request> requests;
  late http.Response Function(http.Request) answer;

  http.Response stored(
    String id, {
    String? title,
    String message = 'hi',
    int priority = 3,
  }) => http.Response(
    jsonEncode({
      'id': id,
      'time': 1700000000,
      'event': 'message',
      'topic': 'alerts',
      'title': ?title,
      'message': message,
      'priority': priority,
    }),
    200,
  );

  setUp(() async {
    db = memoryDb();
    requests = [];
    answer = (_) => stored(mid(1));
    container = ProviderContainer(
      overrides: [
        databaseProvider.overrideWithValue(db),
        secretStoreProvider.overrideWithValue(MemorySecretStore()),
        connectionManagerProvider.overrideWithValue(SpyManager(topicsOf(db))),
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
    await topicsOf(db)
        .subscribe(ServerAddress.parse('https://example.com')!, 'alerts');
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  Publisher publisher() => container.read(publisherProvider);

  test('sends, and stores what the server stored as the user\'s own', () async {
    answer = (_) => stored(mid(7), title: 'Hi', message: 'hello', priority: 4);
    await publisher().send(
      serverId: 1,
      topic: 'alerts',
      text: 'hello',
      title: 'Hi',
      priority: 4,
    );

    expect(requests.single.url.toString(), 'https://example.com/v1/messages');
    final row = await db.select(db.messages).getSingle();
    expect(
      (row.id, row.title, row.body, row.priority, row.mine),
      (mid(7), 'Hi', 'hello', 4, true),
    );
  });

  test('is signed in when the user is', () async {
    await container
        .read(credentialsProvider.notifier)
        .save(
          'https://example.com',
          const Credentials(token: 'flr_secret', tokenId: 'tok', user: 'alice'),
        );
    await publisher().send(serverId: 1, topic: 'alerts', text: 'hi');
    expect(requests.single.headers['Authorization'], 'Bearer flr_secret');
  });

  test('is anonymous when the user is not signed in', () async {
    await publisher().send(serverId: 1, topic: 'alerts', text: 'hi');
    expect(requests.single.headers.containsKey('Authorization'), isFalse);
  });

  test('stores nothing when the server refuses', () async {
    answer = (_) => http.Response(
      jsonEncode({'code': 40301, 'http': 403, 'error': 'no'}),
      403,
    );

    await expectLater(
      publisher().send(serverId: 1, topic: 'alerts', text: 'hi'),
      throwsA(
        isA<ApiException>().having(
          (e) => e.failure,
          'failure',
          ApiFailure.forbidden,
        ),
      ),
    );
    expect(await db.select(db.messages).get(), isEmpty);
  });

  test(
    'marks a message that the live connection delivered first as the user\'s',
    () async {
      await container
          .read(messagesRepositoryProvider)
          .store(incoming(1, 'alerts', 5, body: 'hi'));
      answer = (_) => stored(mid(5));

      await publisher().send(serverId: 1, topic: 'alerts', text: 'hi');

      final rows = await db.select(db.messages).get();
      expect(rows, hasLength(1));
      expect(rows.single.mine, isTrue);
    },
  );

  test('says so when the server is not one the app knows', () async {
    await expectLater(
      publisher().send(serverId: 99, topic: 'alerts', text: 'hi'),
      throwsA(isA<ApiException>()),
    );
    expect(requests, isEmpty);
  });
}

TopicsRepository topicsOf(AppDatabase db) => TopicsRepository(db);
