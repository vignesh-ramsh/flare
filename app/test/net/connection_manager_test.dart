import 'dart:math';

import 'package:app/core/db/database.dart';
import 'package:app/data/server_url.dart';
import 'package:app/net/backoff.dart';
import 'package:app/net/connection_manager.dart';
import 'package:app/net/server_connection.dart';
import 'package:app/net/server_status.dart';
import 'package:drift/drift.dart' show Value;
import 'package:flutter_test/flutter_test.dart';

import '../data/test_db.dart' show mid;
import 'fake_server.dart';
import 'harness.dart';

void main() {
  late Harness h;
  late ConnectionManager manager;

  setUp(() {
    h = Harness();
    manager = ConnectionManager(
      topics: h.topics,
      factory: (ServerRow server) => ServerConnection(
        serverId: server.id,
        url: Uri.parse(server.url),
        store: RepositoryConnectionStore(h.topics, h.messages),
        backoff: Backoff(
          base: const Duration(milliseconds: 20),
          max: const Duration(milliseconds: 100),
          random: Random(1),
        ),
        connectTimeout: const Duration(seconds: 2),
      ),
    );
  });

  tearDown(() async {
    await manager.close();
    await h.dispose();
  });

  ServerAddress address() =>
      ServerAddress.parse('http://127.0.0.1:${h.server.port}')!;

  test('does nothing until started, then follows the subscriptions in the database', () async {
    await h.init(['alerts']);
    h.server.keep(wireMessage('alerts', mid(1)));
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(h.server.attempts, 0);

    manager.start();
    await eventually(() async => (await h.stored('alerts')).length == 1);
  });

  test('follows a subscription added while running', () async {
    await h.init([]);
    manager.start();
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(h.server.attempts, 0);

    h.server.keep(wireMessage('alerts', mid(1)));
    await h.topics.subscribe(address(), 'alerts');
    await eventually(() async => (await h.stored('alerts')).length == 1);
  });

  test('uses one connection for all of a server\'s topics', () async {
    await h.init(['a']);
    manager.start();
    await eventually(() => h.server.sockets.length == 1);

    await h.topics.subscribe(address(), 'b');
    await eventually(() => h.server.subscribesFor('b').isNotEmpty);
    expect(h.server.attempts, 1);
  });

  test('keeps a separate connection and status per server', () async {
    await h.init(['a']);
    final other = FakeFlareServer();
    await other.start();
    addTearDown(other.stop);
    await h.topics.subscribe(
      ServerAddress.parse('http://127.0.0.1:${other.port}')!,
      'b',
    );

    manager.start();
    await eventually(
      () =>
          manager.current.length == 2 &&
          manager.current.values.every((s) => s.state == LinkState.connected),
    );

    expect(h.server.sockets, hasLength(1));
    expect(other.sockets, hasLength(1));
    expect(h.server.subscribesFor('b'), isEmpty);
    expect(other.subscribesFor('a'), isEmpty);
  });

  test('a server that fails does not hold up the others', () async {
    await h.init(['a']);
    final down = FakeFlareServer();
    await down.start();
    final downAddress = ServerAddress.parse('http://127.0.0.1:${down.port}')!;
    await h.topics.subscribe(downAddress, 'b');
    await down.stop();
    h.server.keep(wireMessage('a', mid(1)));

    manager.start();
    await eventually(() async => (await h.stored('a')).length == 1);
    await eventually(
      () => manager.current.values.any((s) => s.state == LinkState.waiting),
    );
  });

  test('reports status changes', () async {
    await h.init(['alerts']);
    final seen = <LinkState>{};
    final sub = manager.statuses.listen(
      (all) => seen.addAll(all.values.map((s) => s.state)),
    );
    manager.start();
    await eventually(() => seen.contains(LinkState.connected));
    await sub.cancel();

    expect(seen, containsAll([LinkState.connecting, LinkState.connected]));
  });

  test('moves to the new address when a server\'s address changes', () async {
    await h.init(['alerts']);
    final moved = FakeFlareServer();
    await moved.start();
    addTearDown(moved.stop);
    manager.start();
    await eventually(() => h.server.sockets.length == 1);

    await (h.db.update(h.db.servers))
        .write(ServersCompanion(url: Value('http://127.0.0.1:${moved.port}')));

    await eventually(
      () => moved.sockets.length == 1,
      reason: 'connected to the new address',
    );
    await eventually(
      () => h.server.sockets.isEmpty,
      reason: 'left the old address',
    );
    await eventually(() => moved.subscribesFor('alerts').length == 1);
  });

  test('reconnectAll tries again at once', () async {
    await h.init(['alerts']);
    final port = h.server.port;
    await h.server.stop();
    manager.start();
    await eventually(
      () => manager.current.values.any((s) => s.state == LinkState.waiting),
    );

    await h.server.start(port: port);
    manager.reconnectAll();
    await eventually(
      () => manager.current.values.every((s) => s.state == LinkState.connected),
    );
  });

  test('close ends every connection', () async {
    await h.init(['alerts']);
    manager.start();
    await eventually(() => h.server.sockets.length == 1);

    await manager.close();
    await eventually(() => h.server.sockets.isEmpty);
    expect(manager.current, isEmpty);
  });
}
