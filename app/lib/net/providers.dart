import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/providers.dart';
import 'connection_manager.dart';
import 'server_connection.dart';
import 'server_status.dart';

/// Follows the subscriptions in the database over the network. Nothing connects
/// until `start()` is called (the app does it at launch); tests leave it alone.
final connectionManagerProvider = Provider<ConnectionManager>((ref) {
  final topics = ref.watch(topicsRepositoryProvider);
  final store = RepositoryConnectionStore(
    topics,
    ref.watch(messagesRepositoryProvider),
  );
  final manager = ConnectionManager(
    topics: topics,
    factory: (server) => ServerConnection(
      serverId: server.id,
      url: Uri.parse(server.url),
      store: store,
    ),
  );
  ref.onDispose(manager.close);
  return manager;
});

/// The state of every server's connection, by server ID.
final connectionStatusProvider = StreamProvider<Map<int, ServerStatus>>((
  ref,
) async* {
  final manager = ref.watch(connectionManagerProvider);
  yield manager.current;
  yield* manager.statuses;
});
