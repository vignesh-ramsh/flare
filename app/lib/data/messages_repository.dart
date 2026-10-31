import 'dart:async';

import 'package:drift/drift.dart';

import '../core/db/database.dart';
import 'models.dart';

/// How long the device keeps messages unless the user changes it.
const defaultHistory = Duration(days: 30);

class MessagesRepository {
  MessagesRepository(this._db);

  final AppDatabase _db;
  final _stored = StreamController<IncomingMessage>.broadcast();

  /// Each message as it is newly stored: not one that was already there, and not one for a
  /// topic the user is not subscribed to. Notifications are driven from this.
  Stream<IncomingMessage> get stored => _stored.stream;

  /// A topic's messages, newest first.
  Stream<List<MessageRow>> watchTopic(
    int serverId,
    String topic, {
    int limit = 500,
  }) {
    final query = _db.select(_db.messages)
      ..where((m) => m.serverId.equals(serverId) & m.topic.equals(topic))
      ..orderBy([(m) => OrderingTerm.desc(m.id)])
      ..limit(limit);
    return query.watch();
  }

  /// Every stored message, newest first.
  Stream<List<InboxItem>> watchInbox({int limit = 200}) {
    final query =
        _db.select(_db.messages).join([
            innerJoin(
              _db.servers,
              _db.servers.id.equalsExp(_db.messages.serverId),
            ),
          ])
          ..orderBy([OrderingTerm.desc(_db.messages.id)])
          ..limit(limit);
    return query.watch().map(
      (rows) => [
        for (final row in rows)
          InboxItem(
            message: row.readTable(_db.messages),
            host: row.readTable(_db.servers).host,
          ),
      ],
    );
  }

  /// Stores a message and moves the topic's cursor forward. Receiving the same
  /// message again, by push and by catch-up, changes nothing. Returns whether
  /// the message was new. Messages for topics the user is not subscribed to are dropped.
  Future<bool> store(IncomingMessage message) async {
    final created = await _insert(message);
    if (created && !_stored.isClosed) _stored.add(message);
    return created;
  }

  Future<bool> _insert(IncomingMessage message) {
    return _db.transaction(() async {
      final topic =
          await (_db.select(_db.topics)..where(
                (t) =>
                    t.serverId.equals(message.serverId) &
                    t.name.equals(message.topic),
              ))
              .getSingleOrNull();
      if (topic == null) return false;

      final known =
          await (_db.select(_db.messages)..where(
                (m) =>
                    m.serverId.equals(message.serverId) &
                    m.id.equals(message.id),
              ))
              .getSingleOrNull();
      if (known != null) return false;

      await _db
          .into(_db.messages)
          .insert(
            MessagesCompanion.insert(
              serverId: message.serverId,
              id: message.id,
              topic: message.topic,
              time: message.time,
              title: Value(message.title),
              body: Value(message.body),
              priority: Value(message.priority),
              tags: Value(message.tags),
              click: Value(message.click),
            ),
          );

      final cursor = topic.cursor;
      if (cursor == null || message.id.compareTo(cursor) > 0) {
        await (_db.update(_db.topics)..where((t) => t.id.equals(topic.id)))
            .write(TopicsCompanion(cursor: Value(message.id)));
      }
      return true;
    });
  }

  /// Deletes messages sent before [cutoff]. Cursors are kept, so nothing is fetched again.
  Future<int> pruneOlderThan(DateTime cutoff) {
    final seconds = cutoff.millisecondsSinceEpoch ~/ 1000;
    return (_db.delete(
      _db.messages,
    )..where((m) => m.time.isSmallerThanValue(seconds))).go();
  }
}
