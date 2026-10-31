import 'package:drift/drift.dart';

import '../core/db/database.dart';

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
