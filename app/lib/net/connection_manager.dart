import 'dart:async';

import '../core/db/database.dart';
import '../data/models.dart';
import '../data/topics_repository.dart';
import 'server_connection.dart';
import 'server_status.dart';

/// Makes a connection for a server. Tests pass their own.
typedef ConnectionFactory = ServerConnection Function(ServerRow server);

/// Keeps one [ServerConnection] per server that has subscriptions, following what is
/// in the database: a new subscription is followed at once, a server with none loses
/// its connection.
class ConnectionManager {
  ConnectionManager({required this._topics, required this._factory});

  final TopicsRepository _topics;
  final ConnectionFactory _factory;

  final Map<int, _Entry> _connections = {};
  final _statuses = StreamController<Map<int, ServerStatus>>.broadcast();
  StreamSubscription<List<FollowedTopic>>? _watch;
  bool _closed = false;

  /// The status of every server that has a connection, by server ID.
  Map<int, ServerStatus> get current => {
    for (final entry in _connections.entries)
      entry.key: entry.value.connection.status,
  };

  /// Changes to [current].
  Stream<Map<int, ServerStatus>> get statuses => _statuses.stream;

  /// Starts following the database. Calling it again does nothing.
  void start() {
    _watch ??= _topics.watchFollowed().listen(_apply);
  }

  /// Tries again now on every server that is waiting, for when the app comes back to
  /// the foreground.
  void reconnectAll() {
    for (final entry in _connections.values) {
      entry.connection.reconnectNow();
    }
  }

  void _apply(List<FollowedTopic> followed) {
    if (_closed) return;
    final byServer = <int, ({ServerRow server, Set<String> topics})>{};
    for (final item in followed) {
      byServer
          .putIfAbsent(
            item.server.id,
            () => (server: item.server, topics: <String>{}),
          )
          .topics
          .add(item.topic);
    }

    for (final id in _connections.keys.toList()) {
      final wanted = byServer[id];
      // A server whose address changed gets a new connection.
      if (wanted == null || wanted.server.url != _connections[id]!.url) {
        unawaited(_connections.remove(id)!.dispose());
      }
    }
    for (final entry in byServer.entries) {
      final existing = _connections[entry.key];
      if (existing != null) {
        existing.connection.setTopics(entry.value.topics);
        continue;
      }
      final connection = _factory(entry.value.server);
      final created = _Entry(
        connection,
        entry.value.server.url,
        connection.statuses.listen((_) => _publish()),
      );
      _connections[entry.key] = created;
      connection
        ..setTopics(entry.value.topics)
        ..start();
    }
    _publish();
  }

  void _publish() {
    if (!_closed) _statuses.add(current);
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    await _watch?.cancel();
    final all = _connections.values.toList();
    _connections.clear();
    await Future.wait([for (final entry in all) entry.dispose()]);
    await _statuses.close();
  }
}

class _Entry {
  _Entry(this.connection, this.url, this.subscription);

  final ServerConnection connection;
  final String url;
  final StreamSubscription<ServerStatus> subscription;

  Future<void> dispose() async {
    await subscription.cancel();
    await connection.close();
  }
}
