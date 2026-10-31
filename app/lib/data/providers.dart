import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../brand/brand.g.dart';
import '../core/db/connection.dart';
import '../core/db/database.dart';
import 'messages_repository.dart';
import 'models.dart';
import 'servers_repository.dart';
import 'topics_repository.dart';

/// The on-device database. Tests override it with an in-memory one.
final databaseProvider = Provider<AppDatabase>((ref) {
  final db = openAppDatabase();
  ref.onDispose(db.close);
  return db;
});

/// The current time. Tests override it.
final clockProvider = Provider<DateTime Function()>((ref) => DateTime.now);

/// Which servers the user may use: whatever the brand allows.
class ServerPolicy {
  const ServerPolicy({required this.defaultUrl, required this.allowCustom});

  /// Pre-filled for new subscriptions; empty if the build has none.
  final String defaultUrl;

  /// False when the build is locked to [defaultUrl].
  final bool allowCustom;
}

final serverPolicyProvider = Provider<ServerPolicy>(
  (ref) => const ServerPolicy(
    defaultUrl: Brand.defaultServerUrl,
    allowCustom: Brand.allowCustomServers,
  ),
);

final serversRepositoryProvider = Provider(
  (ref) => ServersRepository(ref.watch(databaseProvider)),
);
final topicsRepositoryProvider = Provider(
  (ref) => TopicsRepository(ref.watch(databaseProvider)),
);
final messagesRepositoryProvider = Provider(
  (ref) => MessagesRepository(ref.watch(databaseProvider)),
);

final serversProvider = StreamProvider(
  (ref) => ref.watch(serversRepositoryProvider).watchAll(),
);

/// One server by its database ID, or null once it is removed.
final serverProvider = StreamProvider.family<ServerRow?, int>(
  (ref, id) => ref.watch(serversRepositoryProvider).watch(id),
);

final serversWithCountsProvider = StreamProvider(
  (ref) => ref.watch(serversRepositoryProvider).watchWithCounts(),
);

final topicSummariesProvider = StreamProvider(
  (ref) => ref.watch(topicsRepositoryProvider).watchSummaries(),
);

final topicSummaryProvider = StreamProvider.family<TopicSummary?, TopicRef>(
  (ref, topic) => ref.watch(topicsRepositoryProvider).watchSummary(topic),
);

/// A topic's stored messages, newest first.
final topicMessagesProvider =
    StreamProvider.family<List<MessageRow>, ({int serverId, String name})>(
      (ref, topic) => ref
          .watch(messagesRepositoryProvider)
          .watchTopic(topic.serverId, topic.name),
    );

final inboxProvider = StreamProvider(
  (ref) => ref.watch(messagesRepositoryProvider).watchInbox(),
);

/// The server chosen in the filter chips on the topic list; null shows all.
class ServerFilter extends Notifier<int?> {
  @override
  int? build() => null;

  void select(int? serverId) => state = serverId;
}

final serverFilterProvider = NotifierProvider<ServerFilter, int?>(
  ServerFilter.new,
);
