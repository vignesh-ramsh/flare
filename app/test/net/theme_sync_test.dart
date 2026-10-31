import 'dart:async';
import 'dart:convert';

import 'package:app/core/db/database.dart';
import 'package:app/data/providers.dart';
import 'package:app/data/server_url.dart';
import 'package:app/net/api_client.dart';
import 'package:app/net/providers.dart';
import 'package:app/net/theme_sync.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../data/test_db.dart';

Map<String, Object> theme(String name, {String primary = '#8AB4F8'}) => {
  'name': name,
  'brightness': 'dark',
  'colors': {'primary': primary},
};

http.Response list(List<Map<String, Object>> themes, {String? tag}) =>
    http.Response(jsonEncode({'themes': themes}), 200, headers: {'etag': ?tag});

void main() {
  late AppDatabase db;
  late ProviderContainer container;
  late List<http.Request> requests;
  late Future<http.Response> Function(http.Request) answer;
  late DateTime now;
  late ServerRow a;
  late ServerRow b;

  setUp(() async {
    db = memoryDb();
    requests = [];
    now = DateTime(2025, 1, 15, 12);
    answer = (r) async => list([theme('Midnight')], tag: '"v1"');
    container = ProviderContainer(
      overrides: [
        databaseProvider.overrideWithValue(db),
        clockProvider.overrideWithValue(() => now),
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
    a =
        (await container
                .read(serversRepositoryProvider)
                .add(ServerAddress.parse('https://a.example')!))
            .server;
    b =
        (await container
                .read(serversRepositoryProvider)
                .add(ServerAddress.parse('https://b.example')!))
            .server;
    // Something has to listen to the servers for the sync to hear of new ones.
    container.listen(serversProvider, (_, _) {});
    await container.read(serversProvider.future);
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  ThemeSync sync() => container.read(themeSyncProvider);

  Future<Map<String, List<String>>> held() async => {
    for (final e
        in (await container.read(serverThemesRepositoryProvider).watch().first)
            .entries)
      e.key: [for (final t in e.value) t.name],
  };

  Future<void> later() =>
      Future<void>.delayed(const Duration(milliseconds: 50));

  group('asking', () {
    test('asks every server and keeps what each says', () async {
      answer = (r) async => r.url.host == 'a.example'
          ? list([theme('Midnight')], tag: '"a"')
          : list([theme('Paper'), theme('Sepia')], tag: '"b"');
      await sync().refreshAll();
      expect(
        requests.map((r) => r.url.toString()),
        unorderedEquals([
          'https://a.example/v1/themes',
          'https://b.example/v1/themes',
        ]),
      );
      expect(await held(), {
        'a.example': ['Midnight'],
        'b.example': ['Paper', 'Sepia'],
      });
      expect(
        await container.read(serverThemesRepositoryProvider).tagOf(a.id),
        '"a"',
      );
      expect(
        await container.read(serverThemesRepositoryProvider).tagOf(b.id),
        '"b"',
      );
    });

    test('sends the tag it holds the next time, and keeps its list when told nothing changed', () async {
      await sync().sync(a);
      answer = (r) async => http.Response('', 304);
      await sync().sync(a, within: Duration.zero);
      expect(requests.last.headers['If-None-Match'], '"v1"');
      expect(await held(), {
        'a.example': ['Midnight'],
      });
    });

    test('replaces its list when the server\'s has changed, and forgets themes the server dropped', () async {
      await sync().sync(a);
      answer = (r) async => list([theme('Sepia')], tag: '"v2"');
      await sync().sync(a, within: Duration.zero);
      expect(await held(), {
        'a.example': ['Sepia'],
      });
      answer = (r) async => list([], tag: '"v3"');
      await sync().sync(a, within: Duration.zero);
      expect(await held(), isEmpty);
      expect(
        await container.read(serverThemesRepositoryProvider).tagOf(a.id),
        '"v3"',
      );
    });

    test('does not need a sign-in', () async {
      await sync().sync(a);
      expect(requests.single.headers.containsKey('Authorization'), isFalse);
    });
  });

  group('when the server cannot help', () {
    test('keeps the list it had when the server cannot be reached', () async {
      await sync().sync(a);
      answer = (r) async => throw http.ClientException('down');
      await sync().sync(a, within: Duration.zero);
      expect(await held(), {
        'a.example': ['Midnight'],
      });
      expect(
        await container.read(serverThemesRepositoryProvider).tagOf(a.id),
        '"v1"',
      );
    });

    test(
      'keeps it on a server error too, or an answer that is not themes',
      () async {
        await sync().sync(a);
        for (final reply in [
          http.Response('{"code":50001,"http":500,"error":"x"}', 500),
          http.Response('<html>', 200),
        ]) {
          answer = (r) async => reply;
          await sync().sync(a, within: Duration.zero);
          expect(await held(), {
            'a.example': ['Midnight'],
          });
        }
      },
    );

    test('has no themes from a server that does not offer them, and forgets any it had', () async {
      await sync().sync(a);
      answer = (r) async => http.Response(
        '{"code":40401,"http":404,"error":"nothing here"}',
        404,
      );
      await sync().sync(a, within: Duration.zero);
      expect(await held(), isEmpty);
      expect(
        await container.read(serverThemesRepositoryProvider).tagOf(a.id),
        isNull,
      );
    });

    test('one server failing does not stop the others', () async {
      answer = (r) async => r.url.host == 'a.example'
          ? throw http.ClientException('down')
          : list([theme('Paper')], tag: '"b"');
      await sync().refreshAll();
      expect(await held(), {
        'b.example': ['Paper'],
      });
    });
  });

  group('how often', () {
    test('does not ask again within the minute, and does after it', () async {
      await sync().sync(a);
      await sync().sync(a);
      expect(requests, hasLength(1));
      now = now.add(const Duration(seconds: 59));
      await sync().sync(a);
      expect(requests, hasLength(1));
      now = now.add(const Duration(seconds: 2));
      await sync().sync(a);
      expect(requests, hasLength(2));
    });

    test('a shorter wait can be asked for, as when Appearance opens', () async {
      await sync().refreshAll();
      now = now.add(const Duration(seconds: 11));
      await sync().refreshAll(within: const Duration(seconds: 10));
      expect(requests, hasLength(4));
    });

    test(
      'a failure is not counted, so the next attempt is not held back',
      () async {
        answer = (r) async => throw http.ClientException('down');
        await sync().sync(a);
        answer = (r) async => list([theme('Midnight')], tag: '"v1"');
        await sync().sync(a);
        expect(requests, hasLength(2));
        expect(await held(), {
          'a.example': ['Midnight'],
        });
      },
    );

    test('asks a server once at a time', () async {
      final gate = Completer<http.Response>();
      answer = (r) => gate.future;
      final first = sync().sync(a);
      await later();
      final second = sync().sync(a, within: Duration.zero);
      await later();
      expect(requests, hasLength(1));
      gate.complete(list([theme('Midnight')], tag: '"v1"'));
      await Future.wait([first, second]);
      expect(requests, hasLength(1));
    });
  });

  group('servers coming and going', () {
    test(
      'a server added later is asked at once, and the others are not',
      () async {
        sync(); // starts listening
        await later();
        answer = (r) async => list([theme('Added')], tag: '"x"');
        await container
            .read(serversRepositoryProvider)
            .add(ServerAddress.parse('https://c.example')!);
        await later();
        await later();
        expect(requests.map((r) => r.url.host), ['c.example']);
        expect(await held(), {
          'c.example': ['Added'],
        });
      },
    );

    test(
      'servers there from the start are not asked until something asks',
      () async {
        sync();
        await later();
        expect(requests, isEmpty);
      },
    );

    test('a server removed while it is being asked leaves nothing behind and breaks nothing', () async {
      final gate = Completer<http.Response>();
      answer = (r) => gate.future;
      final asking = sync().sync(a);
      await later();
      await container.read(serversRepositoryProvider).remove(a.id);
      gate.complete(list([theme('Late')], tag: '"x"'));
      await asking;
      expect(await held(), isEmpty);
    });

    test('removing a server removes its themes', () async {
      await sync().refreshAll();
      await container.read(serversRepositoryProvider).remove(a.id);
      expect(await held(), {
        'b.example': ['Midnight'],
      });
    });
  });
}
