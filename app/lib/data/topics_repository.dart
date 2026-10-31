import 'package:drift/drift.dart';

import '../core/db/database.dart';
import 'models.dart';
import 'server_url.dart';

/// One row per subscription, newest activity first. Topics without messages come
/// last, by name. `unread` counts messages after the topic's read marker.
const _summarySql = '''
SELECT t.id AS topic_id, t.server_id, s.host, t.name, t.muted, t.min_priority,
  (SELECT COUNT(*) FROM messages m
     WHERE m.server_id = t.server_id AND m.topic = t.name) AS message_count,
  (SELECT COUNT(*) FROM messages m
     WHERE m.server_id = t.server_id AND m.topic = t.name
       AND (t.read_marker IS NULL OR m.id > t.read_marker)) AS unread,
  l.id AS last_id, l.time AS last_time, l.priority AS last_priority,
  l.title AS last_title, l.body AS last_body
FROM topics t
JOIN servers s ON s.id = t.server_id
LEFT JOIN messages l
  ON l.server_id = t.server_id AND l.topic = t.name
 AND l.id = (SELECT MAX(id) FROM messages WHERE server_id = t.server_id AND topic = t.name)
''';

const _summaryOrder = 'ORDER BY l.id IS NULL, l.id DESC, t.name COLLATE NOCASE';

class TopicsRepository {
  TopicsRepository(this._db);

  final AppDatabase _db;

  Stream<List<TopicSummary>> watchSummaries() {
    return _db
        .customSelect(
          '$_summarySql $_summaryOrder',
          readsFrom: {_db.topics, _db.servers, _db.messages},
        )
        .watch()
        .map((rows) => rows.map(_summaryFrom).toList());
  }

  Stream<TopicSummary?> watchSummary(TopicRef ref) {
    return _db
        .customSelect(
          '$_summarySql WHERE s.host = ?1 AND t.name = ?2',
          variables: [
            Variable.withString(ref.host),
            Variable.withString(ref.name),
          ],
          readsFrom: {_db.topics, _db.servers, _db.messages},
        )
        .watch()
        .map((rows) => rows.isEmpty ? null : _summaryFrom(rows.first));
  }

  /// Every subscription with its server, for the connections to follow.
  Stream<List<FollowedTopic>> watchFollowed() {
    final query = _db.select(_db.topics).join([
      innerJoin(_db.servers, _db.servers.id.equalsExp(_db.topics.serverId)),
    ]);
    return query.watch().map(
      (rows) => [
        for (final row in rows)
          FollowedTopic(
            server: row.readTable(_db.servers),
            topic: row.readTable(_db.topics).name,
          ),
      ],
    );
  }

  /// Subscribes to [topic] on [server], adding the server if it is new.
  /// [topic] must pass [isValidTopic].
  Future<SubscribeResult> subscribe(ServerAddress server, String topic) {
    assert(isValidTopic(topic));
    return _db.transaction(() async {
      final existing = await (_db.select(
        _db.servers,
      )..where((s) => s.host.equals(server.host))).getSingleOrNull();
      final serverId =
          existing?.id ??
          await _db
              .into(_db.servers)
              .insert(
                ServersCompanion.insert(host: server.host, url: server.url),
              );

      final subscribed =
          await (_db.select(_db.topics)..where(
                (t) => t.serverId.equals(serverId) & t.name.equals(topic),
              ))
              .getSingleOrNull();
      if (subscribed != null) return SubscribeResult.alreadySubscribed;

      await _db
          .into(_db.topics)
          .insert(TopicsCompanion.insert(serverId: serverId, name: topic));
      return SubscribeResult.added;
    });
  }

  /// Stops following a topic and deletes the messages stored for it on this device. Nothing
  /// is deleted on the server. The server stays, even with no topics left, so a sign-in is kept.
  Future<void> unsubscribe(int serverId, String name) {
    return _db.transaction(() async {
      await (_db.delete(
        _db.messages,
      )..where((m) => m.serverId.equals(serverId) & m.topic.equals(name))).go();
      await (_db.delete(
        _db.topics,
      )..where((t) => t.serverId.equals(serverId) & t.name.equals(name))).go();
    });
  }

  /// The ID of the newest message received on the topic, or null if none yet.
  Future<String?> cursorOf(int serverId, String name) async {
    final row =
        await (_db.select(_db.topics)
              ..where((t) => t.serverId.equals(serverId) & t.name.equals(name)))
            .getSingleOrNull();
    return row?.cursor;
  }

  /// What a notification needs to know about a topic, or null if the user is not
  /// subscribed to it.
  Future<TopicContext?> contextOf(int serverId, String name) async {
    final query =
        _db.select(_db.topics).join([
          innerJoin(_db.servers, _db.servers.id.equalsExp(_db.topics.serverId)),
        ])..where(
          _db.topics.serverId.equals(serverId) & _db.topics.name.equals(name),
        );
    final row = await query.getSingleOrNull();
    if (row == null) return null;
    final topic = row.readTable(_db.topics);
    return TopicContext(
      topicId: topic.id,
      host: row.readTable(_db.servers).host,
      muted: topic.muted,
      minPriority: topic.minPriority,
    );
  }

  /// A muted topic keeps receiving and storing, and notifies nobody.
  Future<void> setMuted(int topicId, bool muted) =>
      (_db.update(_db.topics)..where((t) => t.id.equals(topicId))).write(
        TopicsCompanion(muted: Value(muted)),
      );

  /// Messages below this priority (1 to 5) are stored but do not notify.
  Future<void> setMinPriority(int topicId, int priority) =>
      (_db.update(_db.topics)..where((t) => t.id.equals(topicId))).write(
        TopicsCompanion(minPriority: Value(priority.clamp(1, 5))),
      );

  /// Moves the topic's read marker to its newest message. It never moves back.
  Future<void> markRead(int serverId, String name) {
    return _db.customUpdate(
      '''
UPDATE topics SET read_marker =
  (SELECT MAX(id) FROM messages WHERE server_id = topics.server_id AND topic = topics.name)
WHERE server_id = ?1 AND name = ?2
  AND (SELECT MAX(id) FROM messages WHERE server_id = topics.server_id AND topic = topics.name)
      > COALESCE(read_marker, '')
''',
      variables: [Variable.withInt(serverId), Variable.withString(name)],
      updates: {_db.topics},
    );
  }

  TopicSummary _summaryFrom(QueryRow row) {
    final lastId = row.readNullable<String>('last_id');
    return TopicSummary(
      topicId: row.read<int>('topic_id'),
      serverId: row.read<int>('server_id'),
      host: row.read<String>('host'),
      name: row.read<String>('name'),
      muted: row.read<bool>('muted'),
      minPriority: row.read<int>('min_priority'),
      unread: row.read<int>('unread'),
      messageCount: row.read<int>('message_count'),
      last: lastId == null
          ? null
          : LastMessage(
              id: lastId,
              time: DateTime.fromMillisecondsSinceEpoch(
                row.read<int>('last_time') * 1000,
              ),
              priority: row.read<int>('last_priority'),
              title: row.readNullable<String>('last_title'),
              body: row.read<String>('last_body'),
            ),
    );
  }
}
