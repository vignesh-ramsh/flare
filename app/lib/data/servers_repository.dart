import 'package:drift/drift.dart';

import '../core/db/database.dart';
import 'server_url.dart';

class ServerWithCount {
  const ServerWithCount({required this.server, required this.topics});

  final ServerRow server;
  final int topics;
}

class ServersRepository {
  ServersRepository(this._db);

  final AppDatabase _db;

  Stream<List<ServerRow>> watchAll() {
    final query = _db.select(_db.servers)
      ..orderBy([
        (s) => OrderingTerm.asc(s.createdAt),
        (s) => OrderingTerm.asc(s.id),
      ]);
    return query.watch();
  }

  /// Adds a server and returns it. If it was already there, returns the existing one
  /// and `created` is false.
  Future<({ServerRow server, bool created})> add(ServerAddress address) {
    return _db.transaction(() async {
      final existing = await (_db.select(
        _db.servers,
      )..where((s) => s.host.equals(address.host))).getSingleOrNull();
      if (existing != null) return (server: existing, created: false);
      final id = await _db
          .into(_db.servers)
          .insert(
            ServersCompanion.insert(host: address.host, url: address.url),
          );
      final row = await (_db.select(
        _db.servers,
      )..where((s) => s.id.equals(id))).getSingle();
      return (server: row, created: true);
    });
  }

  Future<ServerRow?> find(int id) => (_db.select(
    _db.servers,
  )..where((s) => s.id.equals(id))).getSingleOrNull();

  Stream<ServerRow?> watch(int id) => (_db.select(
    _db.servers,
  )..where((s) => s.id.equals(id))).watchSingleOrNull();

  /// Removes a server with its topics and their messages (the database cascades).
  Future<void> remove(int id) =>
      (_db.delete(_db.servers)..where((s) => s.id.equals(id))).go();

  Stream<List<ServerWithCount>> watchWithCounts() {
    final count = _db.topics.id.count();
    final query =
        _db.select(_db.servers).join([
            leftOuterJoin(
              _db.topics,
              _db.topics.serverId.equalsExp(_db.servers.id),
            ),
          ])
          ..addColumns([count])
          ..groupBy([_db.servers.id])
          ..orderBy([OrderingTerm.asc(_db.servers.id)]);
    return query.watch().map(
      (rows) => [
        for (final row in rows)
          ServerWithCount(
            server: row.readTable(_db.servers),
            topics: row.read(count) ?? 0,
          ),
      ],
    );
  }
}
