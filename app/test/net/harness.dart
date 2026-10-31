import 'dart:math';

import 'package:app/core/db/database.dart';
import 'package:app/data/messages_repository.dart';
import 'package:app/data/server_url.dart';
import 'package:app/data/topics_repository.dart';
import 'package:app/net/backoff.dart';
import 'package:app/net/server_connection.dart';
import 'package:drift/drift.dart' show OrderingTerm;
import 'package:drift/native.dart';

import 'fake_server.dart';

/// A database, a fake server and a connection between them.
class Harness {
  final db = AppDatabase(NativeDatabase.memory());
  final server = FakeFlareServer();
  late final TopicsRepository topics = TopicsRepository(db);
  late final MessagesRepository messages = MessagesRepository(db);
  late final int serverId;
  ServerConnection? connection;

  /// Starts the server and subscribes to [followed] on it.
  Future<void> init(List<String> followed, {int? port}) async {
    await server.start(port: port ?? 0);
    final address = ServerAddress.parse('http://127.0.0.1:${server.port}')!;
    await db
        .into(db.servers)
        .insert(ServersCompanion.insert(host: address.host, url: address.url));
    for (final topic in followed) {
      await topics.subscribe(address, topic);
    }
    serverId = (await db.select(db.servers).getSingle()).id;
  }

  /// A connection to the fake server with short delays, so tests are quick.
  ServerConnection connect({
    required Set<String> topics,
    TokenSource? token,
    Duration relagDelay = const Duration(milliseconds: 20),
    Duration stableAfter = const Duration(seconds: 10),
  }) {
    final c = ServerConnection(
      serverId: serverId,
      url: server.url,
      store: RepositoryConnectionStore(this.topics, messages),
      token: token,
      backoff: Backoff(
        base: const Duration(milliseconds: 20),
        max: const Duration(milliseconds: 100),
        random: Random(1),
      ),
      connectTimeout: const Duration(seconds: 2),
      pingInterval: const Duration(seconds: 5),
      relagDelay: relagDelay,
      stableAfter: stableAfter,
    )..setTopics(topics);
    connection = c;
    return c;
  }

  /// IDs stored for a topic, oldest first.
  Future<List<String>> stored(String topic) async {
    final rows =
        await (db.select(db.messages)
              ..where((m) => m.topic.equals(topic))
              ..orderBy([(m) => OrderingTerm.asc(m.id)]))
            .get();
    return [for (final row in rows) row.id];
  }

  Future<String?> cursor(String topic) => topics.cursorOf(serverId, topic);

  Future<void> dispose() async {
    await connection?.close();
    await server.stop();
    await db.close();
  }
}
