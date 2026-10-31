import 'dart:io';
import 'dart:math';

import 'package:app/data/models.dart';
import 'package:app/net/backoff.dart';
import 'package:app/net/server_connection.dart';
import 'package:app/net/server_status.dart';
import 'package:flutter_test/flutter_test.dart';

import '../data/test_db.dart' show mid;
import 'fake_server.dart';
import 'harness.dart';

void main() {
  late Harness h;

  setUp(() => h = Harness());
  tearDown(() => h.dispose());

  Future<void> connected(ServerConnection c) => eventually(
    () => c.status.state == LinkState.connected,
    reason: 'connected, was ${c.status.state}',
  );

  group('following topics', () {
    test(
      'subscribes from the start the first time, and stores the backlog',
      () async {
        await h.init(['alerts']);
        h.server.keep(wireMessage('alerts', mid(1), title: 'one'));
        h.server.keep(wireMessage('alerts', mid(2), title: 'two'));

        final c = h.connect(topics: {'alerts'})..start();
        await eventually(() async => (await h.stored('alerts')).length == 2);

        expect(h.server.subscribesFor('alerts').single['since'], 'all');
        expect(await h.stored('alerts'), [mid(1), mid(2)]);
        expect(await h.cursor('alerts'), mid(2));
        expect(c.status.topics['alerts']!.live, isTrue);
      },
    );

    test('stores messages that arrive while connected', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await eventually(() => c.status.topics['alerts']?.live ?? false);

      h.server.publish(
        wireMessage(
          'alerts',
          mid(1),
          title: 'Disk',
          message: 'full',
          priority: 4,
          tags: ['db'],
        ),
      );
      await eventually(() async => (await h.stored('alerts')).isNotEmpty);

      final row = await (h.db.select(h.db.messages)).getSingle();
      expect((row.title, row.body, row.priority), ('Disk', 'full', 4));
      expect(row.tags, ['db']);
    });

    test('stores messages in the order they arrive, so the cursor ends on the last', () async {
      await h.init(['alerts']);
      for (var i = 1; i <= 50; i++) {
        h.server.keep(wireMessage('alerts', mid(i)));
      }
      h.connect(topics: {'alerts'}).start();
      await eventually(() async => (await h.stored('alerts')).length == 50);

      expect(await h.cursor('alerts'), mid(50));
    });

    test('follows several topics on one connection', () async {
      await h.init(['a', 'b']);
      h.server.keep(wireMessage('a', mid(1)));
      h.server.keep(wireMessage('b', mid(2)));

      h.connect(topics: {'a', 'b'}).start();
      await eventually(
        () async =>
            (await h.stored('a')).length + (await h.stored('b')).length == 2,
      );

      expect(h.server.attempts, 1);
    });

    test('ignores a message for a topic it does not follow', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      h.server.sendToAll(wireMessage('other', mid(1)));
      h.server.sendToAll(wireMessage('alerts', mid(2)));
      await eventually(() async => (await h.stored('alerts')).length == 1);

      expect(await h.stored('other'), isEmpty);
    });

    test('sends the login as a bearer token', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'}, token: () async => 'flr_secret')
        ..start();
      await connected(c);

      expect(
        h.server.requestHeaders.single['authorization'],
        'Bearer flr_secret',
      );
    });

    test('connects anonymously without a login', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      expect(
        h.server.requestHeaders.single.containsKey('authorization'),
        isFalse,
      );
    });

    test('survives junk frames', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      h.server.sendRawToAll('not json');
      h.server.sendRawToAll('{"event":"from_the_future","x":1}');
      h.server.sendToAll({'event': 'message', 'topic': 'alerts'}); // no id
      h.server.sendToAll(wireMessage('alerts', mid(1)));
      await eventually(() async => (await h.stored('alerts')).length == 1);

      expect(c.status.state, LinkState.connected);
    });
  });

  group('history on a first fetch', () {
    Future<List<IncomingMessage>> collect() async {
      final events = <IncomingMessage>[];
      h.messages.stored.listen(events.add);
      return events;
    }

    int nowSeconds([int ago = 0]) =>
        DateTime.now().millisecondsSinceEpoch ~/ 1000 - ago;

    test('is silent when the topic is new to the app, but a message that is new is not', () async {
      await h.init(['alerts']);
      final events = await collect();
      h.server.keep(wireMessage('alerts', mid(1), time: nowSeconds(3600)));
      h.server.keep(wireMessage('alerts', mid(2), time: nowSeconds(120)));
      final c = h.connect(topics: {'alerts'})..start();
      await eventually(() => events.length == 2);
      await connected(c);

      h.server.publish(wireMessage('alerts', mid(3), time: nowSeconds()));
      await eventually(() => events.length == 3);

      expect(events.map((m) => m.silent), [true, true, false]);
    });

    test(
      'is not silent for a topic the app already has a cursor for',
      () async {
        await h.init(['alerts']);
        final events = await collect();
        h.server.keep(wireMessage('alerts', mid(1), time: nowSeconds(3600)));
        final c = h.connect(topics: {'alerts'})..start();
        await eventually(() => events.length == 1);
        expect(events.single.silent, isTrue);

        // The app was away; what it missed is news even though it is old.
        h.server.keep(wireMessage('alerts', mid(2), time: nowSeconds(7200)));
        h.server.dropConnections();
        await eventually(() => events.length == 2);
        expect(events.last.silent, isFalse);
        await connected(c);
      },
    );

    test('treats a message just published as news on a new topic', () async {
      await h.init(['alerts']);
      final events = await collect();
      h.server.keep(wireMessage('alerts', mid(1), time: nowSeconds(2)));
      h.connect(topics: {'alerts'}).start();
      await eventually(() => events.length == 1);
      expect(events.single.silent, isFalse);
    });
  });

  group('changing topics', () {
    test('holds no connection while there is nothing to follow', () async {
      await h.init([]);
      final c = h.connect(topics: {})..start();
      await Future<void>.delayed(const Duration(milliseconds: 150));

      expect(h.server.attempts, 0);
      expect(c.status.state, LinkState.idle);
    });

    test('connects when the first topic is added', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {})..start();
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(h.server.attempts, 0);

      c.setTopics({'alerts'});
      await connected(c);
      await eventually(() => h.server.subscribesFor('alerts').length == 1);
    });

    test(
      'follows a topic added while connected, and stops following one removed',
      () async {
        await h.init(['a', 'b']);
        final c = h.connect(topics: {'a'})..start();
        await connected(c);

        c.setTopics({'a', 'b'});
        await eventually(() => c.status.topics['b']?.live ?? false);
        expect(h.server.attempts, 1, reason: 'the same connection is used');

        c.setTopics({'b'});
        await eventually(
          () => h.server.received.any(
            (f) => f['op'] == 'unsubscribe' && f['topic'] == 'a',
          ),
        );
        expect(c.status.topics.keys, ['b']);
      },
    );

    test('closes the connection when the last topic goes', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      c.setTopics({});
      await eventually(() => c.status.state == LinkState.idle);
      await eventually(() => h.server.sockets.isEmpty);
    });

    test(
      'a topic added after earlier messages starts from the beginning',
      () async {
        await h.init(['a', 'b']);
        h.server.keep(wireMessage('b', mid(1)));
        final c = h.connect(topics: {'a'})..start();
        await connected(c);

        c.setTopics({'a', 'b'});
        await eventually(() async => (await h.stored('b')).length == 1);
      },
    );
  });

  group('reconnecting', () {
    test(
      'catches up from the cursor after the connection drops, with no repeats',
      () async {
        await h.init(['alerts']);
        h.server.keep(wireMessage('alerts', mid(1)));
        final c = h.connect(topics: {'alerts'})..start();
        await eventually(() async => (await h.stored('alerts')).length == 1);

        h.server.keep(
          wireMessage('alerts', mid(2)),
        ); // published while nobody listened
        h.server.dropConnections();
        await eventually(() async => (await h.stored('alerts')).length == 2);

        final since = h.server
            .subscribesFor('alerts')
            .map((f) => f['since'])
            .toList();
        expect(since, ['all', mid(1)]);
        expect(await h.stored('alerts'), [mid(1), mid(2)]);
        expect(await h.cursor('alerts'), mid(2));
        expect(c.status.state, LinkState.connected);
      },
    );

    test(
      'keeps trying while the server is down, and recovers when it returns',
      () async {
        await h.init(['alerts']);
        final port = h.server.port;
        final c = h.connect(topics: {'alerts'})..start();
        await connected(c);

        await h.server.stop();
        await eventually(
          () => c.status.state == LinkState.waiting,
          reason: 'waiting',
        );
        await eventually(
          () => c.status.failures >= 3,
          reason: 'failures counted',
        );
        expect(c.status.retryAt, isNotNull);

        h.server.keep(wireMessage('alerts', mid(1)));
        await h.server.start(port: port);
        await eventually(() async => (await h.stored('alerts')).length == 1);
        expect(c.status.state, LinkState.connected);
        expect(c.status.failures, 0);
      },
    );

    test('waits longer after each failure', () async {
      await h.init(['alerts']);
      await h.server.stop();
      final c = h.connect(topics: {'alerts'})..start();

      final waits = <Duration>[];
      final sub = c.statuses.listen((s) {
        if (s.state == LinkState.waiting && s.retryAt != null) {
          waits.add(s.retryAt!.difference(DateTime.now()));
        }
      });
      await eventually(() => waits.length >= 5, reason: 'five waits');
      await sub.cancel();

      // The cap is 100 ms, and the first wait is at most the base of 20 ms.
      expect(waits.first, lessThanOrEqualTo(const Duration(milliseconds: 25)));
      expect(
        waits.every((d) => d <= const Duration(milliseconds: 105)),
        isTrue,
      );
    });

    test('reconnectNow skips the wait', () async {
      await h.init(['alerts']);
      final port = h.server.port;
      await h.server.stop();
      final c = h.connect(topics: {'alerts'})..start();
      await eventually(() => c.status.state == LinkState.waiting);

      await h.server.start(port: port);
      c.reconnectNow();
      await connected(c);
    });

    test(
      'a connection that held counts as healthy, so the next retry is quick',
      () async {
        await h.init(['alerts']);
        final port = h.server.port;
        await h.server.stop();
        final c = h.connect(
          topics: {'alerts'},
          stableAfter: const Duration(milliseconds: 100),
        )..start();
        await eventually(
          () => c.status.failures >= 4,
          reason: 'four failures, so the wait has grown',
        );

        await h.server.start(port: port);
        await connected(c);
        await Future<void>.delayed(
          const Duration(milliseconds: 150),
        ); // longer than stableAfter

        final waits = <Duration>[];
        final sub = c.statuses.listen((s) {
          if (s.state == LinkState.waiting) {
            waits.add(s.retryAt!.difference(DateTime.now()));
          }
        });
        h.server.dropConnections();
        await eventually(() => waits.isNotEmpty);
        await sub.cancel();

        // Back to the base of 20 ms, not the 50 to 100 ms a fifth failure would wait.
        expect(
          waits.first,
          lessThanOrEqualTo(const Duration(milliseconds: 25)),
        );
      },
    );

    test('a connection that drops at once keeps the wait growing', () async {
      await h.init(['alerts']);
      final waits = <Duration>[];
      final c = h.connect(topics: {'alerts'})..start();
      final sub = c.statuses.listen((s) {
        if (s.state == LinkState.waiting) {
          waits.add(s.retryAt!.difference(DateTime.now()));
        }
      });
      for (var i = 0; i < 6; i++) {
        await connected(c);
        h.server.dropConnections();
        await eventually(() => waits.length > i, reason: 'drop $i noticed');
      }
      await sub.cancel();

      expect(waits.last, greaterThan(const Duration(milliseconds: 40)));
    });
    test(
      'finishes storing what it received before it asks again from the cursor',
      () async {
        await h.init(['alerts']);
        for (var i = 1; i <= 5; i++) {
          h.server.keep(wireMessage('alerts', mid(i)));
        }
        final slow = _SlowStore(
          RepositoryConnectionStore(h.topics, h.messages),
        );
        final c = ServerConnection(
          serverId: h.serverId,
          url: h.server.url,
          store: slow,
          backoff: Backoff(
            base: const Duration(milliseconds: 10),
            max: const Duration(milliseconds: 50),
            random: Random(1),
          ),
        )..start();
        h.connection = c;
        c.setTopics({'alerts'});

        await eventually(() => h.server.subscribesFor('alerts').isNotEmpty);
        await eventually(
          () => slow.started >= 1,
        ); // storing has begun, and is slow
        h.server.dropConnections();
        await eventually(() => h.server.subscribesFor('alerts').length == 2);

        // The second subscribe starts after the fifth message, not part-way through.
        expect(h.server.subscribesFor('alerts').last['since'], mid(5));
      },
    );
  });

  group('when the login changes', () {
    test(
      'reconnects at once with the new login, without counting a failure',
      () async {
        await h.init(['alerts']);
        String? token;
        final c = h.connect(topics: {'alerts'}, token: () async => token)
          ..start();
        await connected(c);
        expect(
          h.server.requestHeaders.single.containsKey('authorization'),
          isFalse,
        );

        final states = <LinkState>[];
        final sub = c.statuses.listen((s) => states.add(s.state));
        token = 'flr_new';
        c.loginChanged();
        await eventually(
          () => h.server.requestHeaders.length == 2,
          reason: 'second connection',
        );
        await connected(c);
        await sub.cancel();

        expect(h.server.requestHeaders.last['authorization'], 'Bearer flr_new');
        expect(states, isNot(contains(LinkState.waiting)));
        expect(c.status.failures, 0);
        await eventually(
          () => h.server.sockets.length == 1,
          reason: 'the old connection closed',
        );
      },
    );

    test('follows the topics again on the new connection', () async {
      await h.init(['a', 'b']);
      final c = h.connect(topics: {'a', 'b'})..start();
      await connected(c);
      await eventually(() => c.status.topics.length == 2);

      c.loginChanged();
      await eventually(
        () =>
            h.server.subscribesFor('a').length == 2 &&
            h.server.subscribesFor('b').length == 2,
      );
    });

    test('tries a refused login again at once', () async {
      await h.init(['alerts']);
      h.server.refuseUpgradeWith = 401;
      var token = 'flr_old';
      final c = h.connect(topics: {'alerts'}, token: () async => token)
        ..start();
      await eventually(() => c.status.state == LinkState.authFailed);

      h.server.refuseUpgradeWith = null;
      token = 'flr_new';
      c.loginChanged();
      await connected(c);
      expect(h.server.requestHeaders.last['authorization'], 'Bearer flr_new');
    });

    test('does not wait out a retry delay', () async {
      await h.init(['alerts']);
      final port = h.server.port;
      await h.server.stop();
      final c = h.connect(topics: {'alerts'})..start();
      await eventually(() => c.status.state == LinkState.waiting);

      await h.server.start(port: port);
      c.loginChanged();
      await connected(c);
    });

    test('opens nothing when there is nothing to follow', () async {
      await h.init([]);
      final c = h.connect(topics: {})..start();
      c.loginChanged();
      await Future<void>.delayed(const Duration(milliseconds: 100));

      expect(h.server.attempts, 0);
      expect(c.status.state, LinkState.idle);
    });

    test('uses the new login even when it changed while the connection was opening', () async {
      await h.init(['alerts']);
      var token = 'flr_old';
      var signedIn = false;
      late ServerConnection c;
      c = h.connect(
        topics: {'alerts'},
        token: () async {
          final current = token;
          // The sign-in happens right after the token was read, before the socket is open.
          if (!signedIn) {
            signedIn = true;
            Future<void>.delayed(Duration.zero, () {
              token = 'flr_new';
              c.loginChanged();
            });
          }
          return current;
        },
      )..start();

      await eventually(
        () =>
            h.server.requestHeaders.isNotEmpty &&
            h.server.requestHeaders.last['authorization'] == 'Bearer flr_new',
        reason: 'reconnected with the new login',
      );
      await connected(c);
    });
  });

  group('when the server says no', () {
    test('a topic it refuses is marked, and the others keep working', () async {
      await h.init(['secret', 'open']);
      h.server.errorOnSubscribe['secret'] = 40301;
      h.server.keep(wireMessage('open', mid(1)));
      final c = h.connect(topics: {'secret', 'open'})..start();

      await eventually(() async => (await h.stored('open')).length == 1);
      await eventually(() => c.status.topics['secret']?.errorCode == 40301);
      expect(c.status.topics['open']!.live, isTrue);
      expect(c.status.state, LinkState.connected);
    });

    test(
      'does not ask again for a refused topic on the same connection',
      () async {
        await h.init(['secret', 'open']);
        h.server.errorOnSubscribe['secret'] = 40101;
        final c = h.connect(topics: {'secret', 'open'})..start();
        await eventually(() => c.status.topics['secret']?.errorCode == 40101);

        c.setTopics({'secret', 'open', 'third'});
        await Future<void>.delayed(const Duration(milliseconds: 100));
        expect(h.server.subscribesFor('secret'), hasLength(1));
      },
    );

    test(
      'asks again on a new connection, because access may have changed',
      () async {
        await h.init(['secret']);
        h.server.errorOnSubscribe['secret'] = 40301;
        final c = h.connect(topics: {'secret'})..start();
        await eventually(() => c.status.topics['secret']?.errorCode == 40301);

        h.server.errorOnSubscribe.clear();
        h.server.dropConnections();
        await eventually(() => c.status.topics['secret']?.live ?? false);
      },
    );

    test('follows a topic again from the cursor when the server dropped it for being slow', () async {
      await h.init(['alerts']);
      h.server.keep(wireMessage('alerts', mid(1)));
      final c = h.connect(topics: {'alerts'})..start();
      await eventually(() async => (await h.stored('alerts')).length == 1);

      h.server.keep(wireMessage('alerts', mid(2)));
      h.server.sendToAll({
        'event': 'error',
        'code': 50002,
        'error': 'slow',
        'topic': 'alerts',
      });
      // The fake still thinks the socket follows the topic; the client resubscribes on its own.
      await eventually(() async => (await h.stored('alerts')).length == 2);

      expect(h.server.subscribesFor('alerts').map((f) => f['since']), [
        'all',
        mid(1),
      ]);
      expect(c.status.state, LinkState.connected);
    });

    test(
      'stops retrying when the server rejects the login at connect',
      () async {
        await h.init(['alerts']);
        h.server.refuseUpgradeWith = 401;
        final c = h.connect(topics: {'alerts'}, token: () async => 'flr_old')
          ..start();

        await eventually(() => c.status.state == LinkState.authFailed);
        final attempts = h.server.attempts;
        await Future<void>.delayed(const Duration(milliseconds: 200));
        expect(h.server.attempts, attempts, reason: 'no more attempts');
      },
    );

    test(
      'tries again after a rejected login once told the login changed',
      () async {
        await h.init(['alerts']);
        h.server.refuseUpgradeWith = 401;
        var token = 'flr_old';
        final c = h.connect(topics: {'alerts'}, token: () async => token)
          ..start();
        await eventually(() => c.status.state == LinkState.authFailed);

        h.server.refuseUpgradeWith = null;
        token = 'flr_new';
        c.reconnectNow();
        await connected(c);
        expect(h.server.requestHeaders.last['authorization'], 'Bearer flr_new');
      },
    );

    test('stops retrying when the server ends the connection because the login was withdrawn', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      h.server.dropConnections(WebSocketStatus.policyViolation);
      await eventually(() => c.status.state == LinkState.authFailed);
      final attempts = h.server.attempts;
      await Future<void>.delayed(const Duration(milliseconds: 200));
      expect(h.server.attempts, attempts);
    });

    test('stops retrying on an invalid-credentials error', () async {
      await h.init(['alerts']);
      final c = h.connect(topics: {'alerts'})..start();
      await connected(c);

      h.server.sendToAll({
        'event': 'error',
        'code': 40102,
        'error': 'token revoked',
      });
      await eventually(() => c.status.state == LinkState.authFailed);
    });

    test(
      'a refused connection that is not a login problem is retried',
      () async {
        await h.init(['alerts']);
        h.server.refuseUpgradeWith = 503;
        final c = h.connect(topics: {'alerts'})..start();

        await eventually(() => h.server.attempts >= 3, reason: 'retried');
        expect(c.status.state, isNot(LinkState.authFailed));
      },
    );
  });

  test('close stops everything', () async {
    await h.init(['alerts']);
    final c = h.connect(topics: {'alerts'})..start();
    await connected(c);

    await c.close();
    await eventually(() => h.server.sockets.isEmpty);
    final attempts = h.server.attempts;
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(h.server.attempts, attempts);
  });

  group('websocketUri', () {
    test('maps http and https to ws and wss', () {
      expect(
        websocketUri(Uri.parse('http://localhost:8080')).toString(),
        'ws://localhost:8080/v1/ws',
      );
      expect(
        websocketUri(Uri.parse('https://example.com')).toString(),
        'wss://example.com/v1/ws',
      );
    });

    test('keeps a path prefix', () {
      expect(
        websocketUri(Uri.parse('https://example.com/notify')).toString(),
        'wss://example.com/notify/v1/ws',
      );
      expect(
        websocketUri(Uri.parse('https://example.com/notify/')).toString(),
        'wss://example.com/notify/v1/ws',
      );
    });
  });
}

/// Stores slowly, so messages are still waiting to be stored when a connection ends.
class _SlowStore implements ConnectionStore {
  _SlowStore(this._inner);

  final ConnectionStore _inner;
  int started = 0;

  @override
  Future<bool> storeMessage(IncomingMessage message) async {
    started++;
    await Future<void>.delayed(const Duration(milliseconds: 40));
    return _inner.storeMessage(message);
  }

  @override
  Future<String?> cursorOf(int serverId, String topic) =>
      _inner.cursorOf(serverId, topic);
}
