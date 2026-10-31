import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/credentials.dart';
import '../data/providers.dart';
import 'api_client.dart';
import 'providers.dart';

/// Sends the user's own messages. Failures are [ApiException]s.
class Publisher {
  Publisher(this._ref);

  final Ref _ref;

  /// Publishes [text] to [topic] on a server, signed in if the user is, and stores the server's
  /// copy as the user's own. The live connection delivers the same message too; whichever comes
  /// first is stored, and the other only marks it as theirs.
  Future<void> send({
    required int serverId,
    required String topic,
    required String text,
    String? title,
    int? priority,
  }) async {
    final server = await _ref.read(serversRepositoryProvider).find(serverId);
    if (server == null) throw const ApiException(ApiFailure.notSupported);

    final token = _ref.read(credentialsProvider).byServer[server.url]?.token;
    final sent = await _ref
        .read(apiProvider)
        .publish(
          Uri.parse(server.url),
          topic: topic,
          message: text,
          title: title,
          priority: priority,
          token: token,
        );
    await _ref
        .read(messagesRepositoryProvider)
        .store(sent.toIncoming(serverId, mine: true));
  }
}

final publisherProvider = Provider<Publisher>(Publisher.new);
